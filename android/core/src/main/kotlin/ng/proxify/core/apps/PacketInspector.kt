package ng.proxify.core.apps

import java.nio.ByteBuffer

/**
 * Minimal, allocation-free reading and writing of IP headers.
 *
 * Scope is deliberately tiny: enough to classify a packet and set its DSCP, and
 * not one field more. This code runs on every packet on a 2GB phone, so it
 * parses in place and never copies.
 */
object PacketInspector {

    const val PROTO_TCP = 6
    const val PROTO_UDP = 17

    fun version(packet: ByteBuffer): Int {
        if (packet.remaining() < 1) return 0
        return (packet.get(packet.position()).toInt() ushr 4) and 0x0F
    }

    /** IP protocol number, or -1 if the packet is too short to tell. */
    fun protocol(packet: ByteBuffer): Int {
        val base = packet.position()
        return when (version(packet)) {
            4 -> if (packet.remaining() >= 10) packet.get(base + 9).toInt() and 0xFF else -1
            // IPv6 next-header. Extension headers are not walked: a packet with
            // them classifies as "unknown", which lands on the safe default.
            6 -> if (packet.remaining() >= 7) packet.get(base + 6).toInt() and 0xFF else -1
            else -> -1
        }
    }

    /** Length of the IP header in bytes, or -1 if unknown. */
    fun headerLength(packet: ByteBuffer): Int {
        val base = packet.position()
        return when (version(packet)) {
            4 -> {
                if (packet.remaining() < 1) return -1
                val ihl = packet.get(base).toInt() and 0x0F
                if (ihl < 5) -1 else ihl * 4
            }
            6 -> 40
            else -> -1
        }
    }

    /** Bytes after the IP and transport headers. Negative means unknown. */
    fun payloadLength(packet: ByteBuffer): Int {
        val ipHeader = headerLength(packet)
        if (ipHeader < 0) return -1
        val transport = when (protocol(packet)) {
            PROTO_UDP -> 8
            PROTO_TCP -> tcpHeaderLength(packet, ipHeader)
            else -> return -1
        }
        if (transport < 0) return -1
        return packet.remaining() - ipHeader - transport
    }

    fun destinationPort(packet: ByteBuffer): Int = port(packet, offset = 2)

    fun sourcePort(packet: ByteBuffer): Int = port(packet, offset = 0)

    private fun port(packet: ByteBuffer, offset: Int): Int {
        val proto = protocol(packet)
        if (proto != PROTO_TCP && proto != PROTO_UDP) return -1
        val ipHeader = headerLength(packet)
        if (ipHeader < 0 || packet.remaining() < ipHeader + offset + 2) return -1
        val base = packet.position() + ipHeader + offset
        return ((packet.get(base).toInt() and 0xFF) shl 8) or (packet.get(base + 1).toInt() and 0xFF)
    }

    private fun tcpHeaderLength(packet: ByteBuffer, ipHeaderLength: Int): Int {
        val base = packet.position() + ipHeaderLength
        if (packet.remaining() < ipHeaderLength + 13) return -1
        val dataOffset = (packet.get(base + 12).toInt() ushr 4) and 0x0F
        return if (dataOffset < 5) -1 else dataOffset * 4
    }

    /**
     * Writes the DSCP (top 6 bits of the traffic-class byte), preserving the
     * ECN bits underneath — clobbering those would break congestion signalling,
     * which is the opposite of what this whole feature is for.
     *
     * For IPv4 the header checksum is repaired incrementally (RFC 1624) rather
     * than recomputed; a full recompute per packet is wasted work on a phone.
     *
     * @return true if the packet was modified.
     */
    fun setDscp(packet: ByteBuffer, dscp: Int): Boolean {
        val base = packet.position()
        return when (version(packet)) {
            4 -> {
                if (packet.remaining() < 12) return false
                val old = packet.get(base + 1).toInt() and 0xFF
                val updated = ((dscp and 0x3F) shl 2) or (old and 0x03)
                if (old == updated) return false

                // Word 0 of the header is bytes 0..1, which is what we changed.
                val oldWord = ((packet.get(base).toInt() and 0xFF) shl 8) or old
                val newWord = ((packet.get(base).toInt() and 0xFF) shl 8) or updated
                packet.put(base + 1, updated.toByte())

                val checksumAt = base + 10
                val current = ((packet.get(checksumAt).toInt() and 0xFF) shl 8) or
                    (packet.get(checksumAt + 1).toInt() and 0xFF)
                val repaired = incrementalChecksum(current, oldWord, newWord)
                packet.put(checksumAt, ((repaired ushr 8) and 0xFF).toByte())
                packet.put(checksumAt + 1, (repaired and 0xFF).toByte())
                true
            }

            6 -> {
                // Traffic class spans the low nibble of byte 0 and the high
                // nibble of byte 1. No header checksum to repair.
                if (packet.remaining() < 2) return false
                val b0 = packet.get(base).toInt() and 0xFF
                val b1 = packet.get(base + 1).toInt() and 0xFF
                val ecn = (b1 ushr 4) and 0x03
                val tc = ((dscp and 0x3F) shl 2) or ecn
                packet.put(base, ((b0 and 0xF0) or ((tc ushr 4) and 0x0F)).toByte())
                packet.put(base + 1, (((tc and 0x0F) shl 4) or (b1 and 0x0F)).toByte())
                true
            }

            else -> false
        }
    }

    /** Reads the DSCP value currently on the packet, or -1. */
    fun dscp(packet: ByteBuffer): Int {
        val base = packet.position()
        return when (version(packet)) {
            4 -> if (packet.remaining() >= 2) (packet.get(base + 1).toInt() and 0xFF) ushr 2 else -1
            6 -> {
                if (packet.remaining() < 2) return -1
                val b0 = packet.get(base).toInt() and 0xFF
                val b1 = packet.get(base + 1).toInt() and 0xFF
                (((b0 and 0x0F) shl 4) or ((b1 ushr 4) and 0x0F)) ushr 2
            }
            else -> -1
        }
    }

    /** RFC 1624 incremental checksum update. */
    private fun incrementalChecksum(current: Int, oldWord: Int, newWord: Int): Int {
        var sum = (current.inv() and 0xFFFF) + (oldWord.inv() and 0xFFFF) + newWord
        while (sum shr 16 != 0) {
            sum = (sum and 0xFFFF) + (sum shr 16)
        }
        return sum.inv() and 0xFFFF
    }
}
