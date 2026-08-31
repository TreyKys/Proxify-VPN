# Launch locations

Three live, hosted on AWS Lightsail. The inventory lives in `infra/fleet.json`;
this document is why.

| # | Location | Code | Role | Priority | Host |
|---|---|---|---|---|---|
| 1 | London, UK | `uk-lon-1` | **Default.** Lowest latency + most-wanted foreign IP | 10 | AWS Lightsail `eu-west-2` |
| 2 | Frankfurt, DE | `de-fra-1` | Overflow capacity | 20 | AWS Lightsail `eu-central-1` |
| 3 | Virginia, US | `us-vir-1` | US streaming and services | 40 | AWS Lightsail `us-east-1` |
| — | Lagos, NG | `ng-lag-1` | *not planned — see below* | 90 | *n/a* |
| — | Montreal, CA | `ca-mtl-1` | Diaspora, immigration portals | 50 | *deferred, but live-able* |
| — | Johannesburg, ZA | `za-jnb-1` | African content | 50 | *deferred* |

Three locations launch. The deferred entries are documented decisions in
`infra/fleet.json` (`enabled: false`) waiting on a demand signal, not
forgotten — and Montreal is a step away from live any time we want it (see
below).

## Why AWS

Single provider, one account, one bill, one set of credentials — real
operational simplicity over juggling five smaller hosts. It is also the
default a lot of people reach for regardless of the spreadsheet, and there is
a case for going with what you'll actually operate confidently.

The thing worth being precise about: **not raw EC2.** The brief's original
instruction to avoid AWS/GCP for edge egress is about EC2's on-demand transfer
pricing — roughly $0.09/GB with no allowance, the same ballpark as GCP. Metered
per-gigabyte egress with no bundled allowance is exactly the trap that rule
exists to avoid, and it is still true of raw EC2 today.

**Lightsail is a different product.** It bundles a fixed amount of outbound
data transfer into a flat monthly price — the same shape as Hetzner or
Contabo, just sold by AWS. The Small bundle we're using is around $12/month
for roughly 3TB included, which is the same mechanism that makes Hetzner cheap:
pay once, know the cap, no metering as long as you stay under it.

So: **AWS Lightsail, not AWS EC2.** The distinction is the whole reason this is
safe to commit to rather than a repeat of the mistake the brief warned about.

### The rule that keeps it safe: never let a box hit overage

Lightsail's overage rate for transfer beyond the bundle is $0.09/GB — the
**exact same rate** as raw EC2 on-demand egress. The bundle is what makes
Lightsail cheap; going over it puts you right back in the trap.

That is what `capacity_peers` in `infra/fleet.json` is for. It is set to
roughly (bundle transfer) ÷ 40GB per light user — currently 75 users per Small
bundle — deliberately conservative. The move when a location nears its cap is
`fleet.sh drain` plus either a bigger bundle or a second box in the same
region, never "let it run over and see."

### Scaling up stays on-brand

Because this is real paid infrastructure from day one rather than a credit
that runs out, there's no forced-migration story. Growing a location is
picking a bigger Lightsail bundle — the tiers scale up to 4TB, then higher, at
roughly the same $/TB — or adding a second box in the region and letting the
selector's load-based ordering split traffic between them. Both are `fleet.sh
provision`, nothing more.

At genuinely large volume, Hetzner-class flat pricing (~€4.50 for 20TB) is
still meaningfully cheaper per terabyte than Lightsail's top tiers. That's a
future cost-optimization to revisit once a location is consistently near
capacity, not a launch requirement.

### AWS's acceptable-use policy

AWS prohibits operating **open** proxies — anonymous, unauthenticated relays,
the kind used for abuse and spam. Proxify is not that: every peer is
individually provisioned per account, and nothing forwards traffic for anyone
we haven't authenticated. That said, cloud providers' tolerance for VPN/proxy
services varies by discretion as much as by written policy, so treat this the
same as the abuse-desk risk noted below rather than as a settled question —
worth a compliance read before real volume, not before Phase 0 testing.

### Testing caveat that will mislead you

Cloud IP ranges — AWS's included — are widely flagged by streaming services
and some geo-restricted sites. The Virginia box exists for unblocking, and it
may well fail at that while the tunnel itself works perfectly. Don't conclude
the unblocking feature is broken without retesting on a residential-reputation
network.

## Why Lagos is not planned

It was going to be the differentiator. The reasoning was wrong, in a way worth
recording so nobody re-argues it from scratch.

**A Nigerian in Nigeria already has a Nigerian IP.** They don't need to buy one
from us. The people who'd need a Lagos exit are Nigerians *abroad* reaching
local banking and betting — the diaspora, a much smaller market than the
domestic users this app is built for.

**And the need it served is better met another way.** The reasons to want a
Nigerian origin were betting and banking. Both are solved by **not tunnelling
those apps at all** (see `docs/app-profiles.md`): instant, more reliable than
a Lagos exit, and free. A user who wants Bet9ja to work doesn't need a
Nigerian exit IP — they need Bet9ja to skip the tunnel.

**No provider we'd use can supply one anyway.** AWS has no Nigerian region,
and neither does Alibaba or Oracle. A Nigerian egress IP requires a machine
physically on a Nigerian network holding Nigerian IP space, sourced locally —
the one box in this fleet that would ever be paid for in cash, on metered
Naira-billed bandwidth.

This is a settled decision for launch, not a "revisit later" placeholder. The
entry stays in the fleet file only as a record of why, with the conditions
that would ever justify reopening it (a real diaspora demand signal, and an
IXPN-connected host) — see `docs/app-profiles.md` for the split-tunnelling
approach that replaces it.

## Why these three

**London is the default, not Frankfurt.** Nigeria's submarine cables —
MainOne, Glo-1, WACS, Equiano — land in Portugal and the UK, so Frankfurt is
reached *through* London. London is both the lowest-latency hop from Lagos and
the single most-requested foreign location for this market (diaspora ties, UK
content, UK-based services). Priority 10 on both counts.

**Frankfurt is capacity, not a destination.** Its job is absorbing overflow
once London nears its transfer cap, which is why it sits at priority 20 with
the identical bundle and capacity.

**Virginia over anywhere west.** US streaming and services are the second
most requested. The east coast is roughly 70ms closer to Lagos than the west,
routed via London, and Netflix US works identically from either.

**Montreal** (AWS's `ca-central-1`, not physically Toronto — renamed from an
earlier `ca-tor-1` to avoid claiming a location we don't have) serves a large
and growing Nigerian diaspora and immigration portals that behave badly from
foreign IPs. Live the moment demand justifies it.

**Johannesburg** would be the only African alternative IP worth having — DStv,
Showmax, SuperSport — and gives the product an African story instead of
"another Western VPN." AWS has no Lightsail presence there yet, so it stays on
the paid-alternative list.

## The selector rule that keeps any in-country box safe

Almost every user is in Nigeria. The server selector breaks ties on the
client's country — so if country matching were compared *before* priority,
every user on "auto" would pile onto whichever box happened to be in-country,
regardless of what it cost to run.

It isn't. Priority is compared first. `TestAutoSelectionNeverDefaultsNigerianUsersToAnInCountryBox`
locks this down rather than leaving it as a comment, because it's the rule
that makes it safe to ever add an in-country box again — Lagos or otherwise —
without silently blowing through its bundle.

## Operating the fleet

```bash
./edge/scripts/fleet.sh list                # the inventory
./edge/scripts/fleet.sh provision uk-lon-1  # bootstrap + register over SSH
./edge/scripts/fleet.sh promote uk-lon-1    # draining -> active
./edge/scripts/fleet.sh status              # what the control plane believes
./edge/scripts/fleet.sh drain de-fra-1      # stop new users (e.g. nearing the cap)
./edge/scripts/fleet.sh resync uk-lon-1     # after rebuilding a box
```

Every box registers as **draining** and only goes live when a human promotes
it. A half-built server never receives a real user.

## Adding a location

One entry in `infra/fleet.json` with an unused `/16`, then
`fleet.sh provision <code>`. No code change, no deploy, no app update — the
picker reads the server list from the API.

Order the next ones by demand rather than by guessing. A "request a location"
tap in the app turns that into a signal instead of an argument.

## One thing to watch

**Abuse desks, not uptime, are what kill a location.** VPN egress attracts
DMCA and abuse reports; provider tolerance varies, and some terminate on a
single complaint with no warning. Being on one provider now makes this simpler
to monitor, not harder to survive — keep an eye on it as volume grows.

Sources: [AWS Lightsail pricing](https://aws.amazon.com/lightsail/pricing),
[AWS Acceptable Use Policy](https://aws.amazon.com/aup),
[Vultr Johannesburg](https://www.datacenters.com/vultr-johannesburg).
