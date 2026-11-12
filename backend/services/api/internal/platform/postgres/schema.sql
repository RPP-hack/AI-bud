CREATE TABLE IF NOT EXISTS access_requests (
    id text PRIMARY KEY,
    owner_id text NOT NULL,
    idempotency_key text NOT NULL,
    payload jsonb NOT NULL,
    UNIQUE(owner_id, idempotency_key)
);
CREATE TABLE IF NOT EXISTS access_events (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id text NOT NULL REFERENCES access_requests(id),
    state text NOT NULL,
    ticket_status text NOT NULL,
    version bigint NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS access_events_request ON access_events(request_id);
CREATE SCHEMA IF NOT EXISTS mock_access;
CREATE TABLE IF NOT EXISTS mock_access.tickets (
    operation_id text PRIMARY KEY,
    owner_id text NOT NULL,
    parameters jsonb NOT NULL,
    ticket_id text NOT NULL UNIQUE,
    status text NOT NULL CHECK (status IN ('pending','granted','rejected')),
    created_at timestamptz NOT NULL DEFAULT now()
);
