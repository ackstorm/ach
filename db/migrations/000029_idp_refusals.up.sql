-- Owners the upstream identity provider refused: Dex answered their refresh
-- with the upstream rejection. Every ek_ of a listed owner stops resolving
-- until the owner logs in again, which deletes the row. email is lower-case.
CREATE TABLE idp_refusals (
    email      text PRIMARY KEY,
    refused_at timestamptz NOT NULL DEFAULT now()
);
