CREATE TABLE access_reconciliation_jobs (
    request_id text PRIMARY KEY REFERENCES access_requests(id) ON DELETE CASCADE,
    due_at timestamptz NOT NULL DEFAULT now(),
    lease_token text,
    leased_until timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error_code text NOT NULL DEFAULT '',
    CHECK ((lease_token IS NULL) = (leased_until IS NULL))
);
CREATE INDEX access_reconciliation_jobs_due ON access_reconciliation_jobs(due_at, request_id);
INSERT INTO access_reconciliation_jobs(request_id)
SELECT id FROM access_requests
WHERE payload->>'State' IN ('submitting','unknown')
   OR (payload->>'State' = 'submitted' AND payload->'Ticket'->>'Status' = 'pending');
