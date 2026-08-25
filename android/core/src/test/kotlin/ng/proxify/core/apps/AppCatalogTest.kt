package ng.proxify.core.apps

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * The catalog encodes product promises, so these tests assert the promises
 * rather than the data. If someone later "tidies" a banking app into the tunnel,
 * this is what stops it reaching a user's phone.
 */
class AppCatalogTest {

    @Test
    fun `every banking app bypasses the tunnel`() {
        val tunnelled = AppCatalog.inCategory(AppCategory.BANKING)
            .filter { it.route != Route.BYPASS }
        assertTrue(
            tunnelled.isEmpty(),
            "banking apps must never be tunnelled — a foreign IP gets the account " +
                "frozen, not protected. Offenders: ${tunnelled.map { it.displayName }}",
        )
    }

    @Test
    fun `every betting app bypasses the tunnel`() {
        val tunnelled = AppCatalog.inCategory(AppCategory.BETTING)
            .filter { it.route != Route.BYPASS }
        assertTrue(
            tunnelled.isEmpty(),
            "betting sites geo-lock to Nigeria; a foreign exit blocks them outright. " +
                "Offenders: ${tunnelled.map { it.displayName }}",
        )
    }

    // This is the property that let us drop the Lagos server: the reason to want
    // a Nigerian exit IP was betting and banking, and bypassing serves both
    // better than a Lagos box would have, at no infrastructure cost.
    @Test
    fun `bypassing covers everything a Nigerian exit IP would have been for`() {
        val needsNigerianOrigin = AppCatalog.all.filter {
            it.category == AppCategory.BANKING || it.category == AppCategory.BETTING
        }
        assertTrue(needsNigerianOrigin.isNotEmpty())
        assertTrue(needsNigerianOrigin.all { it.route == Route.BYPASS })
    }

    @Test
    fun `calls are realtime so a busy line does not break them`() {
        val calls = AppCatalog.inCategory(AppCategory.CALLS)
        assertTrue(calls.isNotEmpty())
        assertTrue(
            calls.all { it.trafficClass == TrafficClass.REALTIME },
            "a call queued behind a download is the failure users actually notice",
        )
    }

    @Test
    fun `streaming is tunnelled, because that is where throttling is recovered`() {
        val streaming = AppCatalog.inCategory(AppCategory.VIDEO) +
            AppCatalog.inCategory(AppCategory.MUSIC)
        assertTrue(streaming.isNotEmpty())
        assertTrue(
            streaming.all { it.route == Route.TUNNEL },
            "encrypting streaming is the one honest speed win we have",
        )
        assertTrue(streaming.all { it.trafficClass == TrafficClass.BULK })
    }

    @Test
    fun `games bypass, because a tunnel would only add ping`() {
        val games = AppCatalog.inCategory(AppCategory.GAMING)
        assertTrue(games.isNotEmpty())
        assertTrue(
            games.all { it.route == Route.BYPASS },
            "routing a twitch game through a distant exit makes it worse, and " +
                "claiming otherwise is the kind of thing users measure",
        )
    }

    @Test
    fun `system traffic never outranks a call or a tap`() {
        val system = AppCatalog.inCategory(AppCategory.SYSTEM)
        assertTrue(system.isNotEmpty())

        // Not "all BACKGROUND": a download the user deliberately started is BULK,
        // because they are sitting there waiting for it. An OS update is not.
        // What must hold is that nothing in this category can push ahead of a
        // call or an interactive tap.
        val tooImportant = system.filter {
            it.trafficClass == TrafficClass.REALTIME || it.trafficClass == TrafficClass.INTERACTIVE
        }
        assertTrue(
            tooImportant.isEmpty(),
            "system traffic must yield to the user: ${tooImportant.map { it.displayName }}",
        )
        assertTrue(TrafficClass.BACKGROUND.dscp < TrafficClass.REALTIME.dscp)
    }

    @Test
    fun `unknown apps are protected by default`() {
        val policy = AppCatalog.policyFor("com.some.app.we.have.never.seen")
        assertEquals(Route.TUNNEL, policy.route)
        assertEquals("com.some.app.we.have.never.seen", policy.packageName)
    }

    @Test
    fun `known apps resolve to their policy`() {
        assertEquals(Route.TUNNEL, AppCatalog.policyFor("com.whatsapp").route)
        assertEquals(TrafficClass.REALTIME, AppCatalog.policyFor("com.whatsapp").trafficClass)
        assertEquals(Route.BYPASS, AppCatalog.policyFor("team.opay.pay").route)
        assertEquals(TrafficClass.BULK, AppCatalog.policyFor("com.google.android.youtube").trafficClass)
    }

    @Test
    fun `package names are unique`() {
        val duplicates = AppCatalog.all
            .groupBy { it.packageName }
            .filter { it.value.size > 1 }
        assertTrue(duplicates.isEmpty(), "duplicate package entries: ${duplicates.keys}")
    }

    @Test
    fun `every app has a reason a person could read`() {
        val jargon = listOf("dscp", "tunnel ", "packet", "qdisc", "route ")
        AppCatalog.all.forEach { policy ->
            assertTrue(policy.reason.isNotBlank(), "${policy.displayName} has no reason")
            assertFalse(
                jargon.any { policy.reason.lowercase().contains(it) },
                "${policy.displayName} explains itself in jargon: '${policy.reason}'",
            )
        }
    }

    @Test
    fun `bypass list is exactly the apps that skip the tunnel`() {
        val expected = AppCatalog.all.filter { it.route == Route.BYPASS }.map { it.packageName }
        assertEquals(expected.toSet(), AppCatalog.bypassPackages().toSet())
    }

    // Not a failure — a worklist. Wrong package names cost us the intended
    // policy silently, and banking is where that hurts, so Phase 0 verifies them.
    @Test
    fun `unverified entries are declared rather than hidden`() {
        val unverified = AppCatalog.unverified()
        println("package names to confirm on-device (${unverified.size}):")
        unverified.groupBy { it.category }.forEach { (category, apps) ->
            println("  $category: ${apps.joinToString { it.displayName }}")
        }
        assertTrue(
            unverified.all { it.needsVerification },
            "unverified() must return only flagged entries",
        )
    }

    @Test
    fun `catalog covers the categories people actually use daily`() {
        AppCategory.entries.forEach { category ->
            assertTrue(
                AppCatalog.inCategory(category).isNotEmpty(),
                "no apps catalogued for $category",
            )
        }
    }
}
