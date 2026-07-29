-- 0001_init.sql — Proxify VPN control plane, initial schema.
--
-- Design notes:
--   * peer_assignments is a DESIRED-STATE table. The control plane writes what
--     ought to exist on an edge server; a reconciler pushes that to the edge and
--     records what actually happened. This is what makes §7 idempotent and
--     tolerant of an edge server being down at payment time.
--   * Subscriptions are prepaid time blocks (rows with a window), never a
--     recurring-billing state machine. "Is this user entitled?" is always
--     "does a row exist covering now()?".
--   * We deliberately store no traffic/connection logs here. See
--     docs/logging-policy.md. Anything resembling per-request activity is out
--     of scope for this database by design.

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

-- ---------------------------------------------------------------- users/auth

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         citext UNIQUE,
    phone         text UNIQUE,
    password_hash text NOT NULL,
    status        text NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active', 'suspended', 'deleted')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_identifier_present CHECK (email IS NOT NULL OR phone IS NOT NULL)
);

-- Refresh tokens. Access tokens are stateless JWTs; refresh tokens live here so
-- a device can be revoked server-side.
CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id  uuid,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id) WHERE revoked_at IS NULL;

-- --------------------------------------------------------------------- plans

CREATE TABLE plans (
    code           text PRIMARY KEY,
    name           text NOT NULL,
    duration       interval NOT NULL,
    price_kobo     bigint NOT NULL CHECK (price_kobo >= 0),
    currency       text NOT NULL DEFAULT 'NGN',
    data_cap_bytes bigint,               -- NULL = uncapped
    device_limit   int NOT NULL DEFAULT 1,
    is_free        boolean NOT NULL DEFAULT false,
    active         boolean NOT NULL DEFAULT true,
    sort_order     int NOT NULL DEFAULT 0
);

-- Prices are placeholders pending the pricing decision; see docs/decisions.md.
INSERT INTO plans (code, name, duration, price_kobo, data_cap_bytes, device_limit, is_free, sort_order) VALUES
    ('free',    'Free',        interval '30 days',      0, 2147483648, 1, true,  0),
    ('daily',   'Day Pass',    interval '1 day',    20000,       NULL, 1, false, 1),
    ('weekly',  'Week Pass',   interval '7 days',   70000,       NULL, 2, false, 2),
    ('monthly', 'Month Pass',  interval '30 days', 200000,       NULL, 3, false, 3);

-- ------------------------------------------------------------- subscriptions

-- One row per purchased time block. Overlapping blocks stack (a user who buys a
-- week while a day is live keeps both; entitlement is the union).
CREATE TABLE subscriptions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_code      text NOT NULL REFERENCES plans(code),
    source         text NOT NULL CHECK (source IN ('paystack', 'free', 'manual', 'promo')),
    starts_at      timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,
    data_cap_bytes bigint,
    revoked_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT subscriptions_window CHECK (expires_at > starts_at)
);

CREATE INDEX subscriptions_active_idx
    ON subscriptions (user_id, expires_at DESC)
    WHERE revoked_at IS NULL;

-- ------------------------------------------------------------------ payments

-- provider_ref is the Paystack reference. UNIQUE on it is the idempotency key
-- for webhook replay: Paystack retries, and it retries a lot.
CREATE TABLE payments (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid REFERENCES users(id) ON DELETE SET NULL,
    provider     text NOT NULL DEFAULT 'paystack',
    provider_ref text NOT NULL,
    plan_code    text REFERENCES plans(code),
    amount_kobo  bigint NOT NULL,
    currency     text NOT NULL DEFAULT 'NGN',
    status       text NOT NULL CHECK (status IN ('pending', 'success', 'failed', 'abandoned')),
    payload      jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_ref)
);

-- Raw webhook receipts, kept only long enough to debug delivery problems.
CREATE TABLE webhook_events (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider    text NOT NULL,
    event_id    text NOT NULL,
    event_type  text NOT NULL,
    payload     jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    handled_at  timestamptz,
    UNIQUE (provider, event_id)
);

-- ------------------------------------------------------------------- servers

CREATE TABLE servers (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code            text NOT NULL UNIQUE,          -- 'de-fsn-1', 'ng-lag-1'
    display_name    text NOT NULL,
    country_code    text NOT NULL,                 -- ISO-3166 alpha-2, 'NG'
    region          text NOT NULL,                 -- 'eu-central', 'ng'
    endpoint_host   text NOT NULL,
    endpoint_port   int  NOT NULL DEFAULT 51820,
    public_key      text NOT NULL,                 -- WireGuard server pubkey (base64)
    -- Obfuscation params handed to the client verbatim (Reality pubkey, SNI,
    -- shortId, fallback ports). Shape is versioned in docs/data-path.md.
    obfuscation     jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Tunnel subnet this server hands out, e.g. 10.77.0.0/16. .1 is the server.
    tunnel_subnet   cidr NOT NULL,
    agent_url       text NOT NULL,                 -- https://de-fsn-1.example:8443
    agent_token     text NOT NULL,                 -- shared secret for the edge agent
    status          text NOT NULL DEFAULT 'draining'
                    CHECK (status IN ('active', 'draining', 'down', 'maintenance')),
    capacity_peers  int NOT NULL DEFAULT 500,
    priority        int NOT NULL DEFAULT 100,      -- lower wins in auto-select
    last_seen_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- ------------------------------------------------------------------- devices

CREATE TABLE devices (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        text NOT NULL DEFAULT 'Android device',
    platform    text NOT NULL DEFAULT 'android',
    -- The device generates its own WireGuard keypair and never sends the
    -- private key. Rotation writes a new value here and bumps every assignment.
    public_key  text NOT NULL UNIQUE,
    app_version text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz,
    revoked_at  timestamptz
);

CREATE INDEX devices_user_idx ON devices (user_id) WHERE revoked_at IS NULL;

-- ---------------------------------------------------------- peer assignments

-- The §7 desired-state table.
--
--   pending   -> control plane wants this peer on the edge; not confirmed yet
--   active    -> edge confirmed the peer is installed
--   revoking  -> control plane wants it gone; edge not confirmed yet
--   revoked   -> edge confirmed removal (terminal, row kept for IP cool-down)
--
-- The reconciler moves pending->active and revoking->revoked. Nothing else
-- writes those transitions, so a crashed request never leaves a half-applied
-- peer: worst case the row sits in pending until the next reconcile pass.
CREATE TABLE peer_assignments (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id      uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    server_id      uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    public_key     text NOT NULL,        -- snapshot of the device key at assign time
    -- Set when a rotation replaces public_key, cleared once the edge confirms.
    -- Without this the superseded key would stay installed on the box: a
    -- rotated-away key must stop working, especially when the reason for the
    -- rotation was that it leaked.
    prev_public_key text,
    tunnel_ip      inet NOT NULL,
    state          text NOT NULL DEFAULT 'pending'
                   CHECK (state IN ('pending', 'active', 'revoking', 'revoked')),
    -- Bumped on every desired-state change (key rotation, re-provision). The
    -- edge agent echoes the revision it applied so we can detect staleness.
    revision       bigint NOT NULL DEFAULT 1,
    applied_revision bigint NOT NULL DEFAULT 0,
    attempts       int NOT NULL DEFAULT 0,
    last_error     text,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    activated_at   timestamptz,
    revoked_at     timestamptz
);

-- One live assignment per (device, server). Revoked rows are excluded so a
-- device can be re-provisioned on a server it previously used.
CREATE UNIQUE INDEX peer_assignments_device_server_live_idx
    ON peer_assignments (device_id, server_id)
    WHERE state <> 'revoked';

-- An IP may only be handed to one live peer per server.
CREATE UNIQUE INDEX peer_assignments_server_ip_live_idx
    ON peer_assignments (server_id, tunnel_ip)
    WHERE state <> 'revoked';

-- Reconciler work queue: everything not in a terminal state, oldest first.
CREATE INDEX peer_assignments_pending_idx
    ON peer_assignments (next_attempt_at)
    WHERE state IN ('pending', 'revoking');

CREATE INDEX peer_assignments_server_live_idx
    ON peer_assignments (server_id)
    WHERE state IN ('pending', 'active');
