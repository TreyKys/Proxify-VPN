# Control plane

Go + Postgres. Turns a payment into a live tunnel config on the right edge
server, and back again on expiry.

```bash
make dev-db && make migrate && make server
make test-server
```

## Layout

```
cmd/api        the server (HTTP + reconciler in one process)
cmd/migrate    applies migrations/*.sql, embedded in the binary
internal/
  api          HTTP handlers, auth middleware, rate limiting
  auth         bcrypt passwords, HMAC access tokens, hashed refresh tokens
  provision    the §7 engine, server selection, backoff, reconciler
  edge         the client interface to edge agents (+ an in-memory fake)
  store        Postgres
  payments/paystack   webhook verification and settlement
  wgkey        key validation at the API boundary
```

The reconciler runs in-process. At this scale that is the right call, and when
it outgrows one box it becomes its own deployment with no code change — it
already claims work with `SELECT … FOR UPDATE SKIP LOCKED` rather than
in-memory state.

## API

| Method | Path | Notes |
|---|---|---|
| POST | `/v1/auth/signup` | grants the free plan; phone or email |
| POST | `/v1/auth/login` | |
| POST | `/v1/auth/refresh` | rotates; a replayed token fails and forces a login |
| POST | `/v1/devices` | registers a WireGuard **public** key; idempotent |
| POST | `/v1/devices/{id}/rotate-key` | |
| GET | `/v1/servers` | publishes a load *bucket*, not a peer count |
| GET | `/v1/plans` | |
| **POST** | **`/v1/tunnel/provision`** | the §7 endpoint; idempotent |
| POST | `/v1/tunnel/release` | |
| GET | `/v1/tunnel/status` | is my peer actually installed yet |
| POST | `/v1/payments/initialize` | |
| POST | `/v1/payments/verify` | for when the webhook hasn't landed |
| POST | `/v1/webhooks/paystack` | signature-verified; the only thing that grants time |
| POST | `/v1/admin/*` | operator endpoints; disabled unless `PROXIFY_ADMIN_TOKEN` is set |

## Schema

`migrations/0001_init.sql`, with the reasoning inline. Three decisions carry it:

- **`peer_assignments` is desired state**, not a record of what happened. See
  `docs/provisioning.md`.
- **Subscriptions are prepaid blocks with a window.** "Is this user entitled?" is
  always "does a row cover `now()`?". New purchases stack onto the end of live
  ones, so topping up early never destroys time already paid for.
- **No usage or connection tables.** Deliberate — see `docs/logging-policy.md`.

## Tests

`internal/provision/provision_test.go` runs the §7 flow against a real Postgres
with a fake edge: idempotency, key rotation, failover, total outage and
recovery, expiry, renewal, server switching, address uniqueness, resync, and
rejection handling.

Set `TEST_DATABASE_URL` to run them; without it they skip.
