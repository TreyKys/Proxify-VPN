# The six launch locations

Finalized. The inventory lives in `infra/fleet.json`; this document is why.

| # | Location | Code | Role | Priority | Capacity |
|---|---|---|---|---|---|
| 1 | London, UK | `uk-lon-1` | **Default.** Lowest latency + most-wanted foreign IP | 10 | 500 |
| 2 | Frankfurt, DE | `de-fsn-1` | Bulk capacity, cheapest bandwidth | 20 | 500 |
| 3 | New York, US | `us-nyc-1` | US streaming and services | 40 | 400 |
| 4 | Toronto, CA | `ca-tor-1` | Diaspora, immigration portals | 50 | 300 |
| 5 | Johannesburg, ZA | `za-jnb-1` | African content — DStv, Showmax, SuperSport | 50 | 80 |
| 6 | Lagos, NG | `ng-lag-1` | **The differentiator.** Real Nigerian IP | 90 | 50 |

## Why these six

**London is the default, not Frankfurt.** The brief assumed Germany/Finland was
the closest well-connected region. The cable topology says otherwise: Nigeria's
submarine cables — MainOne, Glo-1, WACS, Equiano — land in Portugal and the UK,
and Frankfurt is reached *through* London. So London is both the lowest-latency
hop from Lagos and the single most-requested foreign location for this market
(diaspora ties, UK content, UK-based services). It earns priority 10 on both
counts.

**Frankfurt is capacity, not a destination.** Hetzner at ~€4.50 for 20TB is
several times cheaper per terabyte than anything else in the fleet. Its job is
absorbing overflow when London fills, which is why it sits at priority 20 with
the same 500-user capacity.

**New York over anywhere west.** US streaming and services are the second most
requested. East coast is roughly 70ms closer to Lagos than the west, routed via
London, and Netflix US works identically from either.

**Toronto** serves a large and growing Nigerian diaspora, plus immigration
portals that behave badly from foreign IPs.

**Johannesburg** is the only African alternative IP worth having: DStv, Showmax
and SuperSport are genuinely popular across the continent. It also gives the
product an African story rather than "another Western VPN with a Lagos box".

**Lagos is the product's edge and its biggest cost.** No incumbent offers a real
Nigerian IP, and it unlocks betting, banking and local content. It is also
metered Nigerian bandwidth billed in Naira, several times the cost per terabyte
of anything else here.

## The rule that protects the Lagos bill

Almost every user is in Nigeria. The server selector breaks ties on the client's
country — so if country matching were compared *before* priority, every single
user on "auto" would land on the most expensive box in the fleet.

It isn't. Priority is compared first, and Lagos sits at 90, the worst in the
fleet. The Lagos IP is something a user picks deliberately; it is never a
default.

That ordering is worth real money, so it is locked down by
`TestAutoSelectionNeverDefaultsNigerianUsersToTheMeteredLagosBox` rather than
left as a comment. Verified live against all six registered: a Nigerian user on
auto gets London, the same user asking for `ng-lag-1` gets Lagos, and switching
back tears the Lagos peer down.

`capacity_peers` is also a **bandwidth** budget here, not a RAM one — roughly
monthly transfer divided by ~40GB per light user. On the metered boxes,
exceeding it means overage charges rather than slowness, which is why
Johannesburg is 80 and Lagos is 50 while Frankfurt is 500.

## Cost

| Location | Provider | Est. monthly |
|---|---|---|
| London | Contabo (Portsmouth), unlimited traffic | ~$6.50 |
| Frankfurt | Hetzner CX22, 20TB | ~$5 |
| New York | BuyVM, unmetered gigabit | ~$3.50 |
| Toronto | OVH Canada | ~$6 |
| Johannesburg | Vultr, metered | ~$6 |
| Lagos | Local DC, metered, Naira | ~$25–40 |
| | **Total** | **~$50–70/mo** |

Capacity across the fleet is about **1,830 light users**. Roughly 40–50
monthly-pass subscribers covers the entire infrastructure bill, so this is
comfortably OpEx rather than a capital constraint. Prices are estimates from
public pricing and should be re-checked at purchase — Lagos especially, where
metered billing makes the real number depend on usage.

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
