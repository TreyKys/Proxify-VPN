package ng.proxify.core.apps

/**
 * The curated policy catalog: what we do with each app people here actually use.
 *
 * Three rules decided every entry below.
 *
 * **1. Bypass anything that geo-checks you.** Nigerian bank apps fraud-flag
 * foreign IPs; Nigerian betting sites refuse non-Nigerian exits outright. Send
 * them direct and they simply work. This is the reason we do not need a Lagos
 * server: a user who wants Bet9ja to work does not need a Nigerian exit IP, they
 * need Bet9ja to not go through the tunnel at all.
 *
 * **2. Tunnel anything a carrier throttles.** Video and music streaming are what
 * DPI throttling targets. Encrypted and obfuscated, it cannot be classified, so
 * it cannot be selectively slowed. This is the one place we genuinely recover
 * speed the user was already paying for.
 *
 * **3. Classify everything so queues stay fair.** A call and a download in the
 * same tunnel are not equal; the call is destroyed by the queueing the download
 * causes. Marking them differently is what keeps the call usable.
 *
 * Entries marked [AppPolicy.needsVerification] carry a best-guess package name.
 * Getting one wrong is not dangerous — the app falls back to [DEFAULT_POLICY]
 * and is tunnelled — but it does mean the intended policy silently does not
 * apply. Verify before launch; see docs/app-profiles.md.
 */
object AppCatalog {

    // ------------------------------------------------------------ banking

    // Bypass, all of them. Nigerian fintech fraud engines treat a sudden London
    // IP as account takeover: at best an OTP challenge, at worst a freeze. This
    // is the category where tunnelling actively harms the user.
    private val banking = listOf(
        app("team.opay.pay", "OPay", AppCategory.BANKING, verify = true),
        app("com.transsnet.palmpay", "PalmPay", AppCategory.BANKING, verify = true),
        app("com.kudabank.app", "Kuda", AppCategory.BANKING, verify = true),
        app("com.moniepoint.personal", "Moniepoint", AppCategory.BANKING, verify = true),
        app("com.gtbank.gtworld", "GTWorld", AppCategory.BANKING, verify = true),
        app("com.accessbank.accessmobile", "Access More", AppCategory.BANKING, verify = true),
        app("com.zenithbank.eazymoney", "Zenith EazyMoney", AppCategory.BANKING, verify = true),
        app("com.ubagroup.mobilebanking", "UBA Mobile", AppCategory.BANKING, verify = true),
        app("com.firstbanknigeria.firstmobile", "FirstMobile", AppCategory.BANKING, verify = true),
        app("com.stanbicibtc.mobile", "Stanbic IBTC", AppCategory.BANKING, verify = true),
        app("ng.piggyvest.app", "PiggyVest", AppCategory.BANKING, verify = true),
        app("com.cowrywise.android", "Cowrywise", AppCategory.BANKING, verify = true),
        app("com.fairmoney.fairmoney", "FairMoney", AppCategory.BANKING, verify = true),
        app("com.getcarbon.carbon", "Carbon", AppCategory.BANKING, verify = true),
        app("com.paystack.consumer", "Paystack", AppCategory.BANKING, verify = true),
        app("com.paypal.android.p2pmobile", "PayPal", AppCategory.BANKING),
        app("com.wise.android", "Wise", AppCategory.BANKING, verify = true),
    )

    // ------------------------------------------------------------ betting

    // Geo-locked to Nigeria. A foreign exit IP does not degrade these — it
    // blocks them. Bypassing is the only policy that works.
    private val betting = listOf(
        app("com.bet9ja.mobile", "Bet9ja", AppCategory.BETTING, verify = true),
        app("com.sportybet.android", "SportyBet", AppCategory.BETTING, verify = true),
        app("com.betking.mobile", "BetKing", AppCategory.BETTING, verify = true),
        app("com.onexbet.mobile", "1xBet", AppCategory.BETTING, verify = true),
        app("com.msport.android", "MSport", AppCategory.BETTING, verify = true),
        app("com.nairabet.mobile", "NairaBet", AppCategory.BETTING, verify = true),
        app("com.betano.ng", "Betano", AppCategory.BETTING, verify = true),
    )

    // ------------------------------------------------------------ calls

    // Realtime class. These are small, frequent packets that a full queue turns
    // into robot voice. Tunnelled, because carriers throttle VoIP too — several
    // Nigerian carriers have historically degraded WhatsApp calls specifically.
    private val calls = listOf(
        app("com.whatsapp", "WhatsApp", AppCategory.CALLS, cls = TrafficClass.REALTIME),
        app("com.whatsapp.w4b", "WhatsApp Business", AppCategory.CALLS, cls = TrafficClass.REALTIME),
        app("us.zoom.videomeetings", "Zoom", AppCategory.CALLS, cls = TrafficClass.REALTIME),
        app("com.google.android.apps.meetings", "Google Meet", AppCategory.CALLS, cls = TrafficClass.REALTIME),
        app("com.microsoft.teams", "Microsoft Teams", AppCategory.CALLS, cls = TrafficClass.REALTIME),
        app("com.skype.raider", "Skype", AppCategory.CALLS, cls = TrafficClass.REALTIME),
        app("com.viber.voip", "Viber", AppCategory.CALLS, cls = TrafficClass.REALTIME),
        app("com.imo.android.imoim", "imo", AppCategory.CALLS, cls = TrafficClass.REALTIME),
        app("com.imo.android.imoimbeta", "imo Lite", AppCategory.CALLS, cls = TrafficClass.REALTIME),
        app("com.discord", "Discord", AppCategory.CALLS, cls = TrafficClass.REALTIME),
        app("com.google.android.apps.tachyon", "Google Duo/Meet", AppCategory.CALLS, cls = TrafficClass.REALTIME),
    )

    // ------------------------------------------------------------ messaging

    private val messaging = listOf(
        app("org.telegram.messenger", "Telegram", AppCategory.MESSAGING),
        app("com.facebook.orca", "Messenger", AppCategory.MESSAGING),
        app("com.facebook.mlite", "Messenger Lite", AppCategory.MESSAGING),
        app("com.snapchat.android", "Snapchat", AppCategory.MESSAGING),
        app("com.google.android.apps.messaging", "Messages", AppCategory.MESSAGING),
        app("com.truecaller", "Truecaller", AppCategory.MESSAGING),
    )

    // ------------------------------------------------------------ social

    private val social = listOf(
        app("com.facebook.katana", "Facebook", AppCategory.SOCIAL),
        app("com.facebook.lite", "Facebook Lite", AppCategory.SOCIAL),
        app("com.instagram.android", "Instagram", AppCategory.SOCIAL, cls = TrafficClass.BULK),
        app("com.instagram.lite", "Instagram Lite", AppCategory.SOCIAL),
        app("com.twitter.android", "X", AppCategory.SOCIAL),
        app("com.linkedin.android", "LinkedIn", AppCategory.SOCIAL),
        app("com.reddit.frontpage", "Reddit", AppCategory.SOCIAL),
        app("com.pinterest", "Pinterest", AppCategory.SOCIAL),
        app("com.instagram.barcelona", "Threads", AppCategory.SOCIAL),
    )

    // ------------------------------------------------------------ video

    // The throttling category, and the one where tunnelling genuinely recovers
    // speed. Bulk class: they buffer, so they can afford to wait behind a call.
    private val video = listOf(
        app("com.google.android.youtube", "YouTube", AppCategory.VIDEO, cls = TrafficClass.BULK),
        app("com.google.android.apps.youtube.music", "YouTube Music", AppCategory.VIDEO, cls = TrafficClass.BULK),
        app("com.zhiliaoapp.musically", "TikTok", AppCategory.VIDEO, cls = TrafficClass.BULK),
        app("com.ss.android.ugc.trill", "TikTok Lite", AppCategory.VIDEO, cls = TrafficClass.BULK),
        app("com.netflix.mediaclient", "Netflix", AppCategory.VIDEO, cls = TrafficClass.BULK),
        app("com.amazon.avod.thirdpartyclient", "Prime Video", AppCategory.VIDEO, cls = TrafficClass.BULK),
        app("com.showmax.app", "Showmax", AppCategory.VIDEO, cls = TrafficClass.BULK, verify = true),
        app("com.dstv.now.android", "DStv", AppCategory.VIDEO, cls = TrafficClass.BULK, verify = true),
        app("tv.twitch.android.app", "Twitch", AppCategory.VIDEO, cls = TrafficClass.BULK),
        app("com.google.android.videos", "Google TV", AppCategory.VIDEO, cls = TrafficClass.BULK),
    )

    // ------------------------------------------------------------ music

    private val music = listOf(
        app("com.spotify.music", "Spotify", AppCategory.MUSIC, cls = TrafficClass.BULK),
        app("com.afmobi.boomplayer", "Boomplay", AppCategory.MUSIC, cls = TrafficClass.BULK, verify = true),
        app("com.audiomack", "Audiomack", AppCategory.MUSIC, cls = TrafficClass.BULK, verify = true),
        app("com.apple.android.music", "Apple Music", AppCategory.MUSIC, cls = TrafficClass.BULK),
        app("deezer.android.app", "Deezer", AppCategory.MUSIC, cls = TrafficClass.BULK),
        app("com.soundcloud.android", "SoundCloud", AppCategory.MUSIC, cls = TrafficClass.BULK),
    )

    // ------------------------------------------------------------ gaming

    // Bypassed by default, and this one is counter-intuitive enough to state
    // plainly: for a twitch game, a VPN almost always makes ping WORSE, because
    // every packet takes a detour. We do not tunnel these and pretend otherwise.
    // A user whose carrier blocks a game can opt in per-app.
    private val gaming = listOf(
        app("com.activision.callofduty.shooter", "Call of Duty Mobile", AppCategory.GAMING, Route.BYPASS, TrafficClass.REALTIME),
        app("com.tencent.ig", "PUBG Mobile", AppCategory.GAMING, Route.BYPASS, TrafficClass.REALTIME),
        app("com.dts.freefireth", "Free Fire", AppCategory.GAMING, Route.BYPASS, TrafficClass.REALTIME),
        app("com.dts.freefiremax", "Free Fire MAX", AppCategory.GAMING, Route.BYPASS, TrafficClass.REALTIME),
        app("com.ea.gp.fifamobile", "EA FC Mobile", AppCategory.GAMING, Route.BYPASS, TrafficClass.REALTIME),
        app("jp.konami.pesam", "eFootball", AppCategory.GAMING, Route.BYPASS, TrafficClass.REALTIME),
        app("com.firsttouchgames.dls7", "Dream League Soccer", AppCategory.GAMING, Route.BYPASS, TrafficClass.REALTIME),
        app("com.supercell.clashofclans", "Clash of Clans", AppCategory.GAMING, Route.BYPASS, TrafficClass.REALTIME),
        app("com.roblox.client", "Roblox", AppCategory.GAMING, Route.BYPASS, TrafficClass.REALTIME),
        app("com.mobile.legends", "Mobile Legends", AppCategory.GAMING, Route.BYPASS, TrafficClass.REALTIME),
    )

    // ------------------------------------------------------------ commerce

    private val commerce = listOf(
        app("com.jumia.android", "Jumia", AppCategory.COMMERCE, verify = true),
        app("com.konga.android", "Konga", AppCategory.COMMERCE, verify = true),
        app("com.einnovation.temu", "Temu", AppCategory.COMMERCE),
        app("com.zzkko", "SHEIN", AppCategory.COMMERCE),
        app("com.alibaba.aliexpresshd", "AliExpress", AppCategory.COMMERCE),
        app("com.amazon.mShop.android.shopping", "Amazon", AppCategory.COMMERCE),
    )

    // ------------------------------------------------------------ rides

    // Bypassed: these are location and identity sensitive, and a foreign exit
    // makes a ride app think you are in another country. Nothing to gain.
    private val rides = listOf(
        app("ee.mtakso.client", "Bolt", AppCategory.RIDES, Route.BYPASS),
        app("com.ubercab", "Uber", AppCategory.RIDES, Route.BYPASS),
        app("sinet.startup.inDriver", "inDrive", AppCategory.RIDES, Route.BYPASS),
        app("com.chowdeck.customer", "Chowdeck", AppCategory.RIDES, Route.BYPASS, verify = true),
        app("com.glovo", "Glovo", AppCategory.RIDES, Route.BYPASS, verify = true),
        app("com.google.android.apps.maps", "Google Maps", AppCategory.RIDES, Route.BYPASS),
    )

    // ------------------------------------------------------------ crypto

    private val crypto = listOf(
        app("com.binance.dev", "Binance", AppCategory.CRYPTO),
        app("co.bitx.android.wallet", "Luno", AppCategory.CRYPTO, verify = true),
        app("com.quidax.app", "Quidax", AppCategory.CRYPTO, verify = true),
        app("com.bybit.app", "Bybit", AppCategory.CRYPTO, verify = true),
        app("org.toshi", "Coinbase Wallet", AppCategory.CRYPTO),
    )

    // ------------------------------------------------------------ work

    private val work = listOf(
        app("com.google.android.gm", "Gmail", AppCategory.WORK),
        app("com.microsoft.office.outlook", "Outlook", AppCategory.WORK),
        app("com.Slack", "Slack", AppCategory.WORK),
        app("com.google.android.apps.docs", "Google Drive", AppCategory.WORK, cls = TrafficClass.BULK),
        app("com.dropbox.android", "Dropbox", AppCategory.WORK, cls = TrafficClass.BACKGROUND),
        app("com.google.android.apps.docs.editors.docs", "Google Docs", AppCategory.WORK),
        app("com.notion.id", "Notion", AppCategory.WORK),
    )

    // ------------------------------------------------------------ browsers

    private val browsers = listOf(
        app("com.android.chrome", "Chrome", AppCategory.BROWSER),
        app("com.opera.mini.native", "Opera Mini", AppCategory.BROWSER),
        app("com.opera.browser", "Opera", AppCategory.BROWSER),
        app("com.opera.gx", "Opera GX", AppCategory.BROWSER),
        app("com.UCMobile.intl", "UC Browser", AppCategory.BROWSER),
        app("org.mozilla.firefox", "Firefox", AppCategory.BROWSER),
        app("com.brave.browser", "Brave", AppCategory.BROWSER),
        app("com.microsoft.emmx", "Edge", AppCategory.BROWSER),
        app("com.transsion.phoenix", "Phoenix Browser", AppCategory.BROWSER, verify = true),
    )

    // ------------------------------------------------------------ system

    // Background class. An OS update should never be the reason a video call
    // stutters, and on a metered Nigerian data plan it should never be the
    // reason the bundle runs out mid-month either.
    private val system = listOf(
        app("com.android.vending", "Play Store", AppCategory.SYSTEM, cls = TrafficClass.BACKGROUND),
        app("com.google.android.gms", "Google Play Services", AppCategory.SYSTEM, cls = TrafficClass.BACKGROUND),
        app("com.google.android.apps.photos", "Google Photos", AppCategory.SYSTEM, cls = TrafficClass.BACKGROUND),
        app("com.android.providers.downloads", "Downloads", AppCategory.SYSTEM, cls = TrafficClass.BULK),
    )

    /** Every policy, in one list. */
    val all: List<AppPolicy> =
        banking + betting + calls + messaging + social + video + music +
            gaming + commerce + rides + crypto + work + browsers + system

    private val byPackage: Map<String, AppPolicy> = all.associateBy { it.packageName }

    /** The policy for [packageName], or the protect-by-default policy. */
    fun policyFor(packageName: String): AppPolicy =
        byPackage[packageName] ?: DEFAULT_POLICY.copy(packageName = packageName)

    /** Packages that must skip the tunnel. Feeds VpnService's disallow list. */
    fun bypassPackages(): List<String> =
        all.filter { it.route == Route.BYPASS }.map { it.packageName }

    fun inCategory(category: AppCategory): List<AppPolicy> =
        all.filter { it.category == category }

    /** Entries whose package name still needs confirming on a real device. */
    fun unverified(): List<AppPolicy> = all.filter { it.needsVerification }

    private fun app(
        packageName: String,
        displayName: String,
        category: AppCategory,
        route: Route = defaultRouteFor(category),
        cls: TrafficClass = defaultClassFor(category),
        verify: Boolean = false,
    ) = AppPolicy(
        packageName = packageName,
        displayName = displayName,
        category = category,
        route = route,
        trafficClass = cls,
        reason = reasonFor(category, route),
        needsVerification = verify,
    )

    private fun defaultRouteFor(category: AppCategory): Route = when (category) {
        AppCategory.BANKING, AppCategory.BETTING, AppCategory.RIDES -> Route.BYPASS
        else -> Route.TUNNEL
    }

    private fun defaultClassFor(category: AppCategory): TrafficClass = when (category) {
        AppCategory.CALLS, AppCategory.GAMING -> TrafficClass.REALTIME
        AppCategory.VIDEO, AppCategory.MUSIC -> TrafficClass.BULK
        AppCategory.SYSTEM -> TrafficClass.BACKGROUND
        else -> TrafficClass.INTERACTIVE
    }

    /** User-facing explanation. Shown in the per-app screen, so no jargon. */
    private fun reasonFor(category: AppCategory, route: Route): String = when {
        route == Route.BYPASS && category == AppCategory.BANKING ->
            "Sent directly so your bank doesn't flag a foreign location"
        route == Route.BYPASS && category == AppCategory.BETTING ->
            "Sent directly — these sites only work from a Nigerian connection"
        route == Route.BYPASS && category == AppCategory.RIDES ->
            "Sent directly so your location stays correct"
        route == Route.BYPASS && category == AppCategory.GAMING ->
            "Sent directly — a VPN would add delay to your ping"
        category == AppCategory.VIDEO || category == AppCategory.MUSIC ->
            "Protected, so your network can't single out streaming to slow down"
        category == AppCategory.CALLS ->
            "Protected and prioritised, so calls stay clear when the line is busy"
        category == AppCategory.SYSTEM ->
            "Protected, and kept out of the way of everything else"
        else -> "Protected"
    }
}
