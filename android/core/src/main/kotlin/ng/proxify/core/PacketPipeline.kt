package ng.proxify.core

import java.nio.ByteBuffer

/**
 * The accelerator seam (brief §5).
 *
 * v2 adds a compression + FEC stage between the tun interface and the tunnel.
 * The whole point of defining this now is that adding that stage must be an
 * *insertion*, not a rewrite — so the v1 data path already routes every packet
 * through a pipeline, and v1 simply installs a pass-through.
 *
 * The contract:
 *
 *  - [outbound] runs on packets leaving the tun on their way into the tunnel.
 *  - [inbound] runs on packets arriving from the tunnel on their way to the tun.
 *  - Stages are symmetric: whatever the client's [outbound] does, the edge's
 *    [inbound] undoes, and vice versa. A stage that cannot be undone by the
 *    other end has no business here.
 *  - Stages may buffer and may emit a different number of packets than they
 *    consume — FEC emits parity packets, and a decoder may emit a recovered
 *    packet with no corresponding input. Hence the list return type, which
 *    exists purely so that FEC does not require changing this interface later.
 *  - Stages must not block. Anything expensive belongs on its own thread with
 *    its own queue, behind a stage that hands work off.
 *
 * A [version] is carried so client and edge can refuse to run a pipeline they
 * do not both understand, rather than silently corrupting traffic.
 */
interface PacketStage {
    val name: String

    /** Wire-format version this stage implements. */
    val version: Int

    /** tun -> tunnel. */
    fun outbound(packet: ByteBuffer): List<ByteBuffer>

    /** tunnel -> tun. */
    fun inbound(packet: ByteBuffer): List<ByteBuffer>

    /**
     * Called when the tunnel is re-established. Stateful stages (FEC windows,
     * compression dictionaries) must reset here: after a reconnect the other
     * end has no memory of what we sent, and a stage that assumes otherwise
     * produces garbage that looks exactly like a broken network.
     */
    fun reset() {}
}

/**
 * v1's stage: hands every packet straight through.
 *
 * It exists so the data path has the shape it will need in v2, and so any
 * regression in the plumbing shows up now — while the pipeline is provably a
 * no-op — rather than when there is real processing to blame it on.
 */
object PassThroughStage : PacketStage {
    override val name: String = "passthrough"
    override val version: Int = 1
    override fun outbound(packet: ByteBuffer): List<ByteBuffer> = listOf(packet)
    override fun inbound(packet: ByteBuffer): List<ByteBuffer> = listOf(packet)
}

/**
 * An ordered chain of stages.
 *
 * Inbound runs the chain in reverse, which is what makes a pipeline symmetric
 * by construction: `compress -> fec` on the way out is `fec -> compress` on the
 * way back, and getting that backwards is the single most likely way to break
 * this when the accelerator lands.
 */
class PacketPipeline(private val stages: List<PacketStage> = listOf(PassThroughStage)) {

    val descriptor: String = stages.joinToString(",") { "${it.name}/v${it.version}" }

    /** True when the pipeline does no work, so callers can skip it entirely. */
    val isPassThrough: Boolean = stages.all { it === PassThroughStage }

    fun outbound(packet: ByteBuffer): List<ByteBuffer> {
        if (isPassThrough) return listOf(packet)
        var current = listOf(packet)
        for (stage in stages) {
            current = current.flatMap { stage.outbound(it) }
            if (current.isEmpty()) return emptyList()
        }
        return current
    }

    fun inbound(packet: ByteBuffer): List<ByteBuffer> {
        if (isPassThrough) return listOf(packet)
        var current = listOf(packet)
        for (stage in stages.asReversed()) {
            current = current.flatMap { stage.inbound(it) }
            if (current.isEmpty()) return emptyList()
        }
        return current
    }

    fun reset() = stages.forEach { it.reset() }

    companion object {
        /** The v1 pipeline. v2 replaces this with the accelerator chain. */
        fun passThrough(): PacketPipeline = PacketPipeline(listOf(PassThroughStage))
    }
}
