# Launch locations

Six chosen, four live for Phase 0. The inventory lives in `infra/fleet.json`;
this document is why.

| # | Location | Code | Role | Priority | Host |
|---|---|---|---|---|---|
| 1 | London, UK | `uk-lon-1` | **Default.** Lowest latency + most-wanted foreign IP | 10 | Alibaba `eu-west-1` (free) |
| 2 | Frankfurt, DE | `de-fra-1` | Overflow capacity | 20 | Alibaba `eu-central-1` (free) |
| 3 | Virginia, US | `us-vir-1` | US streaming and services | 40 | Alibaba `us-east-1` (free) |
| — | Lagos, NG | `ng-lag-1` | *dropped — see below* | 90 | *deferred* |
| — | Toronto, CA | `ca-tor-1` | Diaspora, immigration portals | 50 | *deferred* |
| — | Johannesburg, ZA | `za-jnb-1` | African content | 50 | *deferred* |

**Phase 0 launches on three, all free.** Alibaba Cloud credits cover every
location Alibaba actually has a region in. The deferred three are documented
decisions in `infra/fleet.json` (`enabled: false`) waiting on a demand signal,
not forgotten.

## Why Lagos is dropped

It was going to be the differentiator. The reasoning was wrong, in a way worth
recording so nobody re-argues it from scratch.

**A Nigerian in Nigeria already has a Nigerian IP.** They do not need to buy one
from us. The people who need a Lagos exit are Nigerians *abroad* reaching local
banking and betting — the diaspora, which is a different and much smaller
market than the domestic users this app is built for.

**And the need it served is better met another way.** The reasons to want a
Nigerian origin were betting and banking. Both are solved by **not tunnelling
those apps at all** (see `docs/app-profiles.md`): instant, more reliable than a
Lagos exit, and free. A user who wants Bet9ja to work does not need a Nigerian
exit IP — they need Bet9ja to skip the tunnel.

**No provider outside Nigeria can supply one anyway.** Neither Alibaba nor
Oracle has a Nigerian region; a Nigerian egress IP requires a machine on a
Nigerian network holding Nigerian IP space. It would have been the one box paid
for in cash from day one, on metered Naira-billed bandwidth.

If diaspora demand shows up, the entry is still in the fleet file. Two
conditions on bringing it back: a real demand signal, and an **IXPN-connected
host** so domestic traffic peers locally instead of crossing paid transit.

## Why these six

**London is the default, not Frankfurt.** The brief assumed Germany/Finland was
the closest well-connected region. The cable topology says otherwise: Nigeria's
submarine cables — MainOne, Glo-1, WACS, Equiano — land in Portugal and the UK,
and Frankfurt is reached *through* London. So London is both the lowest-latency
hop from Lagos and the single most-requested foreign location for this market
(diaspora ties, UK content, UK-based services). It earns priority 10 on both
counts.

**Frankfurt is capacity, not a destination.** Its job is absorbing overflow when
London fills, which is why it sits at priority 20. Its migration target is
Hetzner at ~€4.50 for 20TB — several times cheaper per terabyte than anything
else available to us — which is what makes it the right home for bulk traffic
once credits end.

**Virginia over anywhere west.** US streaming and services are the second most
requested. The east coast is roughly 70ms closer to Lagos than the west, routed
via London, and Netflix US works identically from either. (Alibaba's US region
is Virginia; the eventual paid move is BuyVM New York, same coast.)

**Toronto** serves a large and growing Nigerian diaspora, plus immigration
portals that behave badly from foreign IPs.

**Johannesburg** is the only African alternative IP worth having: DStv, Showmax
and SuperSport are genuinely popular across the continent. It also gives the
product an African story rather than "another Western VPN with a Lagos box".

## The selector rule that made Lagos safe, and still matters

Almost every user is in Nigeria. The server selector breaks ties on the client's
country — so if country matching were compared *before* priority, every user on
"auto" would land on whichever box happened to be in Nigeria, regardless of what
it cost.

It isn't. Priority is compared first. That was what kept the metered Lagos box
off the default path, and the same rule now protects any future in-country box
we add — so the test stays even though Lagos is deferred.

That ordering is worth real money, so it is locked down by
`TestAutoSelectionNeverDefaultsNigerianUsersToTheMeteredLagosBox` rather than
left as a comment. Verified live against all six registered: a Nigerian user on
auto gets London, the same user explicitly asking for an in-country box gets it,
and switching back tears the old peer down.

`capacity_peers` is also a **bandwidth** budget here, not a RAM one — roughly
monthly transfer divided by ~40GB per light user. On the metered boxes,
exceeding it means overage charges rather than slowness, which is why Lagos is
50 while London is 500. On Alibaba the same logic applies to metered egress —
the credits are the budget, and `capacity_peers` is what keeps a box inside it.

## Hosting: Alibaba Cloud for Phase 0

We run Phase 0 on Alibaba Cloud free credits. With no users and no launch,
spending money to avoid a deferrable cost is backwards, and this codebase makes
migration genuinely cheap — a location is a row in `infra/fleet.json`, and
moving one is `fleet.sh provision` followed by `fleet.sh drain` on the old box.

What the credits can and cannot do:

| | |
|---|---|
| Covered by credits | London, Frankfurt, Virginia |
| **Not available at any price** | **Lagos** — Alibaba has no Nigerian region |
| Not covered | Toronto (no Alibaba region), Johannesburg (BCX reseller only) |

Note the shape of that: the credits cover the three *cheapest* boxes (~$15/mo
combined) and cannot touch the expensive one. Real saving is on the order of
$15/mo against a ~$50–70 fleet, because Lagos is paid from day one regardless.

### Migration targets, decided in advance

So the swap is a decision already made rather than a scramble when credits end:

| Location | Move to | Why |
|---|---|---|
| Frankfurt | Hetzner CX22 | ~€4.50 for 20TB flat — cheapest bandwidth available to us |
| London | Contabo (Portsmouth) | unlimited traffic, fair use |
| Virginia → New York | BuyVM | ~$3.50, unmetered gigabit |

### The three Alibaba-specific traps

**Metered egress is the AWS trap in a different hat.** Alibaba bills outbound
traffic per GB (inbound free), tiered and region-specific — structurally the
same model the brief already rules out for AWS/GCP. Credits hide that until
they expire, which is exactly when you have enough users for it to hurt.

**Set public bandwidth deliberately.** ECS instances default to a very low
public bandwidth cap. Leave it at the default and the VPN will crawl, and it
will look like our tunnel is broken rather than like a billing setting.
`public_bandwidth_mbps` in the fleet file records the intended value.

**Burstable shapes throttle under sustained load.** The lightest instances are
burstable and run on CPU credits. At Phase 0 volumes — a handful of test
devices — that is fine. Under real traffic, credit exhaustion drops you to a
fraction of a core, which presents as the tunnel going slow: precisely the
failure this product exists to avoid. Watch CPU credits before real users
arrive, and move to a non-burstable shape at that point.

### The line not to cross

Free credits for a spike with no users and no real data is a sound call. What
does not migrate cleanly is a launched reputation: our entire pitch is "we log
almost nothing", Alibaba is Chinese-headquartered and subject to China's
National Intelligence and Data Security laws, and VPN review sites dig into
hosting and ownership as a matter of routine. There is also an NDPA
cross-border transfer question once real user data is involved.

So: **Alibaba for Phase 0 testing, off Alibaba before the privacy policy goes
live and before real users' traffic flows.** That timing is a launch blocker in
`docs/roadmap.md`, not a preference.

### Testing caveat that will mislead you

Cloud IP ranges — Alibaba's especially — are widely flagged by streaming
services and some geo-restricted sites. The Virginia box exists for unblocking,
and it may well fail at it while the tunnel itself works perfectly. Do not
conclude the unblocking feature is broken without retesting on a
residential-reputation provider.

## Operating the fleet

```bash
./edge/scripts/fleet.sh list                # the inventory
./edge/scripts/fleet.sh provision uk-lon-1  # bootstrap + register over SSH
./edge/scripts/fleet.sh promote uk-lon-1    # draining -> active
./edge/scripts/fleet.sh status              # what the control plane believes
./edge/scripts/fleet.sh drain za-jnb-1      # stop new users (e.g. nearing the cap)
./edge/scripts/fleet.sh resync ng-lag-1     # after rebuilding a box
```

Every box registers as **draining** and only goes live when a human promotes it.
A half-built server never receives a real user.

## Adding location #7

One entry in `infra/fleet.json` with an unused `/16`, then
`fleet.sh provision <code>`. No code change, no deploy, no app update — the
picker reads the server list from the API.

Order the next ones by demand rather than by guessing. A "request a location"
tap in the app turns that into a signal instead of an argument.

## Two things to watch

**Abuse desks, not uptime, will be what kills a location.** VPN egress attracts
DMCA and abuse reports, and provider tolerance varies enormously — some
terminate on a single complaint with no warning. Six locations means five
providers. Before committing, confirm in writing that each permits VPN egress,
and keep the fleet file's `provider` note current so a terminated box is a
ten-minute replacement rather than an investigation.

**Johannesburg and Lagos are metered.** Set up bandwidth alerts at both, and use
`fleet.sh drain` when one approaches its cap. Draining stops new users while
existing ones keep working — the graceful version of hitting a limit.

Sources for provider availability and pricing:
[HostAdvice — VPS Nigeria](https://hostadvice.com/vps/nigeria/),
[Melbicom Lagos](https://www.melbicom.net/virtualserver/nigeria/),
[telaHosting](https://telahosting.ng/blog/6-best-managed-vps-hosting-providers-in-nigeria/),
[Vultr Johannesburg](https://www.datacenters.com/vultr-johannesburg),
[Contabo VPS](https://contabo.com/en/vps-o/).
