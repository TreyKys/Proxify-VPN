package ng.proxify.core.apps

import java.nio.ByteBuffer
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

/**
 * Header surgery is the one place in this package where a bug corrupts traffic
 * rather than merely misprioritising it, so the checksum maths gets tested
 * against an independent recomputation rather than against itself.
 */
class PacketMarkingTest {

    /** Builds an IPv4 packet with a correct header checksum. */
    private fun ipv4(
        protocol: Int,
        dstPort: Int,
        payloadSize: Int,
        dscp: Int = 0,
        ecn: Int = 0,
    ): ByteBuffer {
        val transportLen = if (protocol == PacketInspector.PROTO_UDP) 8 else 20
        val total = 20 + transportLen + payloadSize
        val b = ByteBuffer.allocate(total)

        b.put(0, 0x45)                                     // v4, IHL 5
        b.put(1, (((dscp and 0x3F) shl 2) or ecn).toByte())
        b.put(2, ((total ushr 8) and 0xFF).toByte())
        b.put(3, (total and 0xFF).toByte())
        b.put(8, 64)                                       // TTL
        b.put(9, protocol.toByte())
        // src 10.0.0.1, dst 10.0.0.2
        b.put(12, 10); b.put(13, 0); b.put(14, 0); b.put(15, 1)
        b.put(16, 10); b.put(17, 0); b.put(18, 0); b.put(19, 2)

        b.put(22, ((dstPort ushr 8) and 0xFF).toByte())
        b.put(23, (dstPort and 0xFF).toByte())
        if (protocol == PacketInspector.PROTO_TCP) {
            b.put(32, 0x50)                                // data offset 5
        }

        val checksum = computeChecksum(b, 20)
        b.put(10, ((checksum ushr 8) and 0xFF).toByte())
        b.put(11, (checksum and 0xFF).toByte())

        b.position(0).limit(total)
        return b
    }

    /** Independent full recomputation, used to verify incremental repair. */
    private fun computeChecksum(b: ByteBuffer, headerLength: Int): Int {
        var sum = 0
        for (i in 0 until headerLength step 2) {
            if (i == 10) continue // skip the checksum field itself
            sum += ((b.get(i).toInt() and 0xFF) shl 8) or (b.get(i + 1).toInt() and 0xFF)
        }
        while (sum shr 16 != 0) sum = (sum and 0xFFFF) + (sum shr 16)
        return sum.inv() and 0xFFFF
    }

    private fun checksumOf(b: ByteBuffer): Int =
        ((b.get(10).toInt() and 0xFF) shl 8) or (b.get(11).toInt() and 0xFF)

    @Test
    fun `parses protocol, port and payload length`() {
        val p = ipv4(PacketInspector.PROTO_UDP, dstPort = 443, payloadSize = 1200)
        assertEquals(4, PacketInspector.version(p))
        assertEquals(PacketInspector.PROTO_UDP, PacketInspector.protocol(p))
        assertEquals(443, PacketInspector.destinationPort(p))
        assertEquals(1200, PacketInspector.payloadLength(p))
    }

    @Test
    fun `marking dscp keeps the header checksum valid`() {
        val p = ipv4(PacketInspector.PROTO_UDP, dstPort = 5004, payloadSize = 160)
        assertTrue(PacketInspector.setDscp(p, TrafficClass.REALTIME.dscp))

        assertEquals(TrafficClass.REALTIME.dscp, PacketInspector.dscp(p))
        assertEquals(
            computeChecksum(p, 20),
            checksumOf(p),
            "incremental checksum repair disagrees with a full recomputation",
        )
    }

    @Test
    fun `marking preserves ecn bits`() {
        // ECN 0b11 is "congestion experienced". Clobbering it would break the
        // congestion signalling this feature depends on.
        val p = ipv4(PacketInspector.PROTO_TCP, dstPort = 443, payloadSize = 1400, ecn = 3)
        PacketInspector.setDscp(p, TrafficClass.BULK.dscp)

        assertEquals(3, p.get(1).toInt() and 0x03, "ECN bits were destroyed")
        assertEquals(computeChecksum(p, 20), checksumOf(p))
    }

    @Test
    fun `marking every class keeps checksums valid`() {
        TrafficClass.entries.forEach { cls ->
            val p = ipv4(PacketInspector.PROTO_TCP, dstPort = 443, payloadSize = 500, dscp = 12)
            PacketInspector.setDscp(p, cls.dscp)
            assertEquals(cls.dscp, PacketInspector.dscp(p))
            assertEquals(computeChecksum(p, 20), checksumOf(p), "bad checksum after marking $cls")
        }
    }

    @Test
    fun `re-marking to the same value leaves the packet untouched`() {
        val p = ipv4(PacketInspector.PROTO_TCP, dstPort = 443, payloadSize = 100, dscp = 34)
        val before = checksumOf(p)
        assertEquals(false, PacketInspector.setDscp(p, 34))
        assertEquals(before, checksumOf(p))
    }

    @Test
    fun `truncated packets are ignored rather than corrupted`() {
        val tiny = ByteBuffer.allocate(3).apply { put(0, 0x45); position(0) }
        assertEquals(false, PacketInspector.setDscp(tiny, TrafficClass.REALTIME.dscp))
        assertEquals(-1, PacketInspector.payloadLength(tiny))
    }

    @Test
    fun `ipv6 traffic class is written without a checksum`() {
        val b = ByteBuffer.allocate(48)
        b.put(0, 0x60)             // v6
        b.put(6, PacketInspector.PROTO_UDP.toByte())
        b.position(0).limit(48)

        assertTrue(PacketInspector.setDscp(b, TrafficClass.REALTIME.dscp))
        assertEquals(TrafficClass.REALTIME.dscp, PacketInspector.dscp(b))
        assertEquals(6, PacketInspector.version(b))
    }

    // ------------------------------------------------------------ classifier

    @Test
    fun `small regular udp is treated as a call`() {
        val voice = ipv4(PacketInspector.PROTO_UDP, dstPort = 5004, payloadSize = 160)
        assertEquals(TrafficClass.REALTIME, TrafficClassifier().classify(voice))
    }

    @Test
    fun `full-size quic is treated as a download, not a call`() {
        // The mistake that would matter: promoting a stream to realtime lets it
        // starve the calls this mechanism exists to protect.
        val quic = ipv4(PacketInspector.PROTO_UDP, dstPort = 443, payloadSize = 1200)
        assertEquals(TrafficClass.BULK, TrafficClassifier().classify(quic))
    }

    @Test
    fun `dns is prioritised because a stalled lookup looks like a dead page`() {
        val dns = ipv4(PacketInspector.PROTO_UDP, dstPort = 53, payloadSize = 40)
        assertEquals(TrafficClass.INTERACTIVE, TrafficClassifier().classify(dns))
    }

    @Test
    fun `full tcp segments are bulk and small ones are interactive`() {
        val bulk = ipv4(PacketInspector.PROTO_TCP, dstPort = 443, payloadSize = 1400)
        val tap = ipv4(PacketInspector.PROTO_TCP, dstPort = 443, payloadSize = 120)
        assertEquals(TrafficClass.BULK, TrafficClassifier().classify(bulk))
        assertEquals(TrafficClass.INTERACTIVE, TrafficClassifier().classify(tap))
    }

    @Test
    fun `app identity overrides packet shape`() {
        // YouTube over QUIC looks identical to a video call over QUIC. Only app
        // identity separates them, so when we have it, it wins.
        val ambiguous = ipv4(PacketInspector.PROTO_UDP, dstPort = 443, payloadSize = 200)
        val heuristic = TrafficClassifier().classify(ambiguous)
        val resolved = TrafficClassifier { TrafficClass.BULK }.classify(ambiguous)

        assertEquals(TrafficClass.REALTIME, heuristic)
        assertEquals(TrafficClass.BULK, resolved)
        assertNotEquals(heuristic, resolved)
    }

    @Test
    fun `the stage marks outbound and leaves inbound alone`() {
        val stage = DscpMarkingStage()

        val out = ipv4(PacketInspector.PROTO_UDP, dstPort = 5004, payloadSize = 160)
        stage.outbound(out)
        assertEquals(TrafficClass.REALTIME.dscp, PacketInspector.dscp(out))

        val incoming = ipv4(PacketInspector.PROTO_UDP, dstPort = 5004, payloadSize = 160, dscp = 7)
        stage.inbound(incoming)
        assertEquals(7, PacketInspector.dscp(incoming), "inbound marking is wasted work")
    }
}
