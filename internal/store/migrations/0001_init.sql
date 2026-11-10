-- Feather Cloud schema v1 (sync-protocol.md). Ids are ULID text; secrets are stored only as SHA-256 hashes.

CREATE TABLE accounts (
    id                      text PRIMARY KEY,
    email_norm              text NOT NULL,
    salt                    bytea NOT NULL,
    kdf                     jsonb NOT NULL,
    auth_hash               bytea NOT NULL,
    recovery_auth_hash      bytea NOT NULL,
    wrapped_master_password bytea NOT NULL,
    wrapped_master_recovery bytea NOT NULL,
    library_id              text NOT NULL,
    plan_state              text NOT NULL CHECK (plan_state IN ('trial', 'active', 'read_only')),
    plan_reason             text NULL CHECK (plan_reason IN ('trial_expired', 'subscription_ended', 'trial_already_used')),
    trial_ends_at           timestamptz NULL,
    last_seq                bigint NOT NULL DEFAULT 0,
    created_at              timestamptz NOT NULL,
    deleted_at              timestamptz NULL
);
CREATE UNIQUE INDEX accounts_email_live ON accounts (email_norm) WHERE deleted_at IS NULL;

CREATE TABLE pending_signups (
    email_norm              text PRIMARY KEY,
    salt                    bytea NOT NULL,
    kdf                     jsonb NOT NULL,
    auth_hash               bytea NOT NULL,
    recovery_auth_hash      bytea NOT NULL,
    wrapped_master_password bytea NOT NULL,
    wrapped_master_recovery bytea NOT NULL,
    library_id              text NOT NULL,
    code_hash               bytea NOT NULL,
    code_sent_at            timestamptz NOT NULL,
    attempts                int NOT NULL DEFAULT 0,
    created_at              timestamptz NOT NULL
);

-- Outlives the account: one trial per email, ever.
CREATE TABLE trial_records (
    email_hash bytea PRIMARY KEY,
    used_at    timestamptz NOT NULL
);

CREATE TABLE devices (
    id           text PRIMARY KEY,
    account_id   text NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    name         text NOT NULL,
    created_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL
);
CREATE INDEX devices_account ON devices (account_id);

CREATE TABLE sessions (
    token_hash   bytea PRIMARY KEY,
    device_id    text NOT NULL UNIQUE REFERENCES devices (id) ON DELETE CASCADE,
    account_id   text NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    scope        text NOT NULL CHECK (scope IN ('full', 'recovery')),
    created_at   timestamptz NOT NULL,
    last_used_at timestamptz NOT NULL
);
CREATE INDEX sessions_account ON sessions (account_id);

CREATE TABLE items (
    account_id  text NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    id          text NOT NULL,
    type        text NOT NULL CHECK (type IN ('note', 'notebook', 'tag', 'attachment')),
    version     bigint NOT NULL,
    seq         bigint NOT NULL,
    deleted     boolean NOT NULL,
    meta        bytea NULL,
    blob_sha256 text NULL,
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (account_id, id),
    UNIQUE (account_id, seq)
);
CREATE INDEX items_blob ON items (account_id, blob_sha256) WHERE blob_sha256 IS NOT NULL;

CREATE TABLE blobs (
    account_id  text NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    sha256      text NOT NULL,
    size        bigint NOT NULL,
    uploaded_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, sha256)
);
