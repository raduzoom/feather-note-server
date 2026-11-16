-- Failed verification checks per email (sync-protocol.md "Limits"): at most 20 in 24 hours. A new signup or a
-- resend replaces the code and resets attempts, but leaves these alone.
ALTER TABLE pending_signups
    ADD COLUMN failures       int NOT NULL DEFAULT 0,
    ADD COLUMN failures_since timestamptz;
