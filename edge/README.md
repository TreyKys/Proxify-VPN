# Edge servers

An edge server terminates user tunnels and forwards to the internet. It runs
three things:

| Component | Role |
|---|---|
| WireGuard (`wg0`) | the tunnel itself, UDP/51820 |
| Xray (VLESS + XTLS Reality) | DPI-resistant path on TCP/443, forwards to the local WireGuard port |
| `edge-agent` | applies the peer set the control plane asks for |

## Provisioning a box

```bash
# On the box, as root, from a checkout of this repo:
make -C edge build                       # produces edge/bin/edge-agent
./edge/scripts/bootstrap.sh \
  --code de-fsn-1 \
  --subnet 10.77.0.0/16 \
  --country DE --region eu-central \
  --hostname de-fsn-1.proxify.ng \
  --control-plane-ip 203.0.113.10
```

The script is idempotent — re-run it after any change. It prints the exact
`curl` needed to register the box with the control plane.

For more than one box, drive it from the inventory instead of by hand:

```bash
./edge/scripts/fleet.sh provision uk-lon-1   # ssh, bootstrap, register
./edge/scripts/fleet.sh promote uk-lon-1
```

`infra/fleet.json` holds every location's subnet, priority and capacity;
`docs/locations.md` explains the six we launch with.

A new box registers as **draining**: reachable and managed, but handed to no
users. Verify it, then flip it to `active`. That ordering exists so a half-built
box never receives real users.

## Why the agent instead of SSH

The control plane needs to add and remove peers thousands of times a day, on
boxes that occasionally have bad routes. A small authenticated HTTP endpoint
gives us bounded timeouts, clean error classification (`unreachable` vs
`rejected` — see `server/internal/edge`), and no long-lived SSH keys to a fleet
of cheap VPSes.

The agent reports an **instance ID** that survives reboots but not rebuilds. When
it changes, the control plane knows this box's peer table is empty and pushes
the full set back. That's what turns "I rebuilt that server and forgot to restore
`/etc/wireguard`" from an outage into a non-event.

## Peer state, and why it is on disk

Peers are applied with `wg set` and persisted with `wg-quick save`. Without the
save, a reboot would disconnect every user on the box until the reconciler
noticed. With it, a reboot is invisible.

## The Reality endpoint is not an open proxy

`config/xray-reality.json.tmpl` routes the Reality inbound to exactly one
destination: `127.0.0.1:<wireguard port>`. Everything else is blackholed. A
leaked UUID therefore buys an attacker a WireGuard handshake they cannot
complete, not free bandwidth through our IP.

Sniffing and access logs are off. The box does not record which sites pass
through it — see `docs/logging-policy.md`.

## Hosting

Three locations on AWS Lightsail — London, Frankfurt, Virginia. `docs/locations.md`
has the reasoning, the pricing, and why it's Lightsail rather than raw EC2.

Never use raw EC2 (or GCP) on-demand transfer for edge egress — no bundled
allowance, ~$0.09/GB, and egress is the main variable cost of this business.
Lightsail sidesteps that by bundling transfer into the plan price the same way
Hetzner does; the rule is "no metered on-demand egress," not "no AWS."
