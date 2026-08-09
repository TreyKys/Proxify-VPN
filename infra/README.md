# Infrastructure

## Local development

```bash
make dev-db      # Postgres in Docker
make migrate     # apply the schema
make server      # run the control plane on :8080
make test        # everything
```

The Android debug build points at `http://10.0.2.2:8080`, which is the host
machine from inside the emulator.

## Production shape

| Piece | Where | Notes |
|---|---|---|
| Control plane | one small VPS behind Cloudflare | stateless; the reconciler uses `SKIP LOCKED`, so a second instance is safe |
| Postgres | managed instance or a dedicated box | daily backups, tested restores |
| Edge servers | six locations, see `docs/locations.md` | driven from `infra/fleet.json` |
| DNS + TLS | Cloudflare free tier | proxied for the API, **not** for the edge endpoints |

Do not put edge egress on AWS/GCP: their bandwidth is ~50–75× Hetzner's, and
egress is this business's main variable cost.

## The fleet

`infra/fleet.json` is the single source of truth for what exists and where.
`edge/scripts/fleet.sh` drives it:

```bash
./edge/scripts/fleet.sh list
./edge/scripts/fleet.sh provision uk-lon-1   # bootstrap + register over SSH
./edge/scripts/fleet.sh promote uk-lon-1     # draining -> active
./edge/scripts/fleet.sh status
```

Adding a location is one entry plus one command — no code change and no app
update. See `docs/locations.md` for the six we launch with and why.

## Scale

One 20TB box carries roughly 500 light users at ~40GB each, so per-user server
cost is on the order of pennies. Infrastructure is cheap OpEx here, not a
capital constraint. Start with two or three boxes and add as users arrive.

## Secrets

`infra/.env.example` lists everything the control plane reads. Nothing in this
repo should ever contain a real key: `.env` is gitignored, the edge agent's
token lives in a 0600 file on the box (not an environment variable, so it stays
out of `ps` output and crash dumps), and server rows hold the agent token in the
database because the control plane needs it to authenticate.

## Monitoring

UptimeRobot on the API and on each edge box. The alert worth building first is
not "is the box up" — it is **rows stuck in `peer_assignments.state = 'pending'`
with a rising `attempts`**, which is what a user experiences as "I paid and it
won't connect".
