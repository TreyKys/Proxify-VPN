# Logging policy

The no-logs claim is a legal commitment and a marketing asset. It is worth
exactly as much as the code behind it, so this document describes what the code
actually does — and it must be updated in the same change as any code that
alters it.

## What we store

| Data | Where | Why |
|---|---|---|
| Email or phone, password hash | `users` | Sign-in |
| Device name, platform, WireGuard **public** key | `devices` | Provisioning |
| Prepaid time blocks (plan, window) | `subscriptions` | Entitlement |
| Payment reference, amount, status | `payments` | Accounting, dispute handling |
| Peer assignment: device, server, tunnel IP, state | `peer_assignments` | The peer must exist somewhere |
| Server inventory | `servers` | Operations |

## What we do not store

- No browsing history, DNS queries, or destination addresses. Anywhere.
- No connection timestamps beyond `last_seen_at` on a device, which is
  overwritten rather than appended.
- No source IP addresses. The API does not log the client IP, and the edge boxes
  do not log connections.
- No per-session records. There is no sessions table and no intent to add one.
- No traffic counters, in v1. See the open question below.

## How the code enforces it

**API request logging** (`server/internal/api/middleware.go`) records method,
path, status, and duration. Not the client IP, not the user agent, not the body.

**Country hints.** `CF-IPCountry` is read to break ties in server selection and
is used within the request. It is never written anywhere.

**Xray on the edge** (`edge/config/xray-reality.json.tmpl`) runs with
`access: "none"`, `sniffing: disabled`, and per-user stats off. Sniffing would
have the box inspect and record destination hostnames, which is precisely what
we promise not to do.

**The edge agent** logs peer applications by tunnel IP and revision, never by
public key, and redacts key-shaped arguments from error messages so they cannot
reach a log or a crash report.

**WireGuard private keys** are generated on the device and never transmitted. We
cannot hand over a key we have never had — under commercial pressure or legal
order alike. That is a structural property, not a promise.

**Webhook bodies** are kept in `webhook_events` to debug delivery problems.
They contain payment metadata, not traffic data, and should be pruned on a
schedule once delivery is stable.

## The open question: free-tier data caps

The free tier is capped by data. Enforcing that requires counting bytes per
user, which is a usage record.

v1 ships **no** usage table, because the schema should not quietly acquire one.
When the cap is implemented, the narrowest honest version is:

- aggregate bytes per peer, per billing block
- no destinations, no timestamps, no per-session breakdown
- reset when the block resets
- stated plainly in the privacy policy: *"For free accounts we count how much
  data you use, so we can apply the cap. We do not record what you connect to."*

Anything richer than that breaks the claim. Decide it together with the privacy
policy, not afterwards.

## Before launch

- [ ] Privacy policy and ToS whose wording matches this document exactly
- [ ] Decide and document the free-tier usage counter (above)
- [ ] Set a retention period for `webhook_events` and enforce it
- [ ] Confirm Sentry (if used) scrubs IPs and user identifiers
- [ ] Confirm the hosting providers' own logging, and what they retain about our
      boxes regardless of what we do
- [ ] Legal review of the Lagos box specifically — see `docs/decisions.md`
