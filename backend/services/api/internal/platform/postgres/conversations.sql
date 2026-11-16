CREATE TABLE conversations (
    id text PRIMARY KEY,
    owner_id text NOT NULL,
    team text NOT NULL,
    role text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    closed_at timestamptz
);
CREATE UNIQUE INDEX conversations_active_owner ON conversations(owner_id) WHERE closed_at IS NULL;
CREATE TABLE conversation_turns (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id text NOT NULL UNIQUE,
    conversation_id text NOT NULL REFERENCES conversations(id),
    idempotency_key text NOT NULL,
    input jsonb NOT NULL,
    state text NOT NULL CHECK (state IN ('pending', 'completed', 'failed')),
    answer jsonb,
    error_code text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(conversation_id, idempotency_key)
);
CREATE INDEX conversation_turns_order ON conversation_turns(conversation_id, sequence);
