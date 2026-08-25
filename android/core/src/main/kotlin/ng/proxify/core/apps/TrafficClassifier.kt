package ng.proxify.core.apps

import ng.proxify.core.PacketStage
import java.nio.ByteBuffer

/**
 * Decides which queue class a packet belongs to.
 *
 * Two sources, in order of trust:
 *
 *  1. **Which app sent it.** Authoritative, because [AppCatalog] already knows
 *     that WhatsApp is a call and YouTube is a stream. The Android layer
 *     resolves a flow to a UID and hands the answer in via [appClassResolver].
 *  2. **What the packet looks like.** A fallback for flows we cannot attribute,
 *     using shape alone — packet size and port. Crude, but a 120-byte UDP
 *     packet on a non-QUIC port is a voice frame far more often than not.
 *
 * The fallback deliberately never guesses [TrafficClass.REALTIME] for anything
 * large. Wrongly promoting a download to realtime priority would let it starve
 * the calls this whole mechanism exists to protect — the one mistake here with
 * a real cost.
 */
class TrafficClassifier(
    /** Resolves a flow to a class using app identity. Null when unattributable. */
    private val appClassResolver: (FlowKey) -> TrafficClass? = { null },
) {
    data class FlowKey(val protocol: Int, val port: Int)

    fun classify(packet: ByteBuffer): TrafficClass {
        val protocol = PacketInspector.protocol(packet)
        if (protocol != PacketInspector.PROTO_TCP && protocol != PacketInspector.PROTO_UDP) {
            return TrafficClass.INTERACTIVE
        }

        val port = PacketInspector.destinationPort(packet)
        appClassResolver(FlowKey(protocol, port))?.let { return it }

        val payload = PacketInspector.payloadLength(packet)
        return when (protocol) {
            PacketInspector.PROTO_UDP -> classifyUdp(port, payload)
            else -> classifyTcp(payload)
        }
    }

    private fun classifyUdp(port: Int, payload: Int): TrafficClass = when {
        // DNS. Tiny, and every stalled lookup is a page that appears to hang,
        // so it goes near the front despite being nobody's idea of realtime.
        port == 53 || port == 853 -> TrafficClass.INTERACTIVE

        // QUIC carrying full-size datagrams is a download wearing UDP's coat.
        port == 443 && payload > QUIC_BULK_THRESHOLD -> TrafficClass.BULK

        // The RTP shape: small, regular datagrams. This is a voice or video call.
        payload in RTP_MIN..RTP_MAX -> TrafficClass.REALTIME

        payload > QUIC_BULK_THRESHOLD -> TrafficClass.BULK
        else -> TrafficClass.INTERACTIVE
    }

    private fun classifyTcp(payload: Int): TrafficClass = when {
        // A full segment means the sender has a queue to drain.
        payload >= TCP_BULK_THRESHOLD -> TrafficClass.BULK
        else -> TrafficClass.INTERACTIVE
    }

    private companion object {
        const val RTP_MIN = 40
        const val RTP_MAX = 400
        const val QUIC_BULK_THRESHOLD = 900
        const val TCP_BULK_THRESHOLD = 1100
    }
}

/**
 * The [PacketStage] that applies the classification.
 *
 * This is the accelerator seam from brief §5 doing real work in v1 — proof the
 * pipeline is a place things can live, not just a comment about v2. Marking is
 * outbound-only: inbound packets have already crossed the network we were
 * trying to influence, so re-marking them would cost CPU for nothing.
 */
class DscpMarkingStage(
    private val classifier: TrafficClassifier = TrafficClassifier(),
) : PacketStage {

    override val name: String = "dscp"
    override val version: Int = 1

    override fun outbound(packet: ByteBuffer): List<ByteBuffer> {
        val cls = classifier.classify(packet)
        PacketInspector.setDscp(packet, cls.dscp)
        return listOf(packet)
    }

    override fun inbound(packet: ByteBuffer): List<ByteBuffer> = listOf(packet)
}
