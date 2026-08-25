package ng.proxify.core.apps

/**
 * Per-app network policy.
 *
 * The honest framing this whole package is built on: **a VPN cannot make a link
 * faster.** It can only stop three specific things from making it slower —
 * carrier throttling, queue starvation, and routing an app somewhere it did not
 * want to go. Every policy below is one of those three, and nothing here claims
 * more than that.
 */
enum class Route {
    /** Through the tunnel: encrypted, unclassifiable by carrier DPI. */
    TUNNEL,

    /**
     * Straight out the normal interface, skipping the tunnel entirely.
     *
     * Not a compromise — for some apps it is strictly better. A Nigerian bank
     * app reached from a London IP gets fraud-flagged or blocked outright, and
     * a betting site that geo-locks to Nigeria refuses a foreign exit. Sending
     * those direct is what makes them work at all.
     */
    BYPASS,
}

/**
 * How a flow should be treated in a queue when the link is busy.
 *
 * This is the mechanism behind "my call didn't break while someone was
 * downloading". It does not create bandwidth; it decides who waits.
 */
enum class TrafficClass(val dscp: Int) {
    /** Voice and video calls. Tiny packets, ruined by 200ms of queueing. */
    REALTIME(46), // EF

    /** Messaging, browsing, anything where a person is waiting on a tap. */
    INTERACTIVE(34), // AF41

    /** Streaming and downloads. Wants throughput, tolerates delay. */
    BULK(0), // best effort

    /** Updates and backups. Should yield to literally everything else. */
    BACKGROUND(8), // CS1
}

enum class AppCategory {
    BANKING,
    BETTING,
    MESSAGING,
    SOCIAL,
    VIDEO,
    MUSIC,
    CALLS,
    GAMING,
    COMMERCE,
    RIDES,
    CRYPTO,
    WORK,
    BROWSER,
    SYSTEM,
}

/**
 * One app's policy.
 *
 * @param needsVerification set where the package name is a best guess rather
 *   than something confirmed on a device. Nigerian banking and betting apps are
 *   the ones that matter here: a wrong package name means the app silently gets
 *   the default policy instead of the one we intended. Confirm with
 *   `adb shell pm list packages` before launch — see docs/app-profiles.md.
 */
data class AppPolicy(
    val packageName: String,
    val displayName: String,
    val category: AppCategory,
    val route: Route,
    val trafficClass: TrafficClass,
    val reason: String,
    val needsVerification: Boolean = false,
)

/**
 * What happens to an app we have never heard of.
 *
 * Tunnel it. A VPN that quietly leaves unknown apps unprotected is a VPN that
 * lies. The cost of being wrong in this direction is an app that misbehaves and
 * a user who taps "send this app direct"; the cost of the opposite is traffic
 * the user believed was protected and wasn't.
 */
val DEFAULT_POLICY = AppPolicy(
    packageName = "",
    displayName = "",
    category = AppCategory.BROWSER,
    route = Route.TUNNEL,
    trafficClass = TrafficClass.INTERACTIVE,
    reason = "Protected by default",
)
