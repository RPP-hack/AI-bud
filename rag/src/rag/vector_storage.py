"""Versioned PostgreSQL/pgvector storage; ACL filtering happens inside SQL."""

from contextlib import contextmanager
import json
import math
import os
import time

from .embeddings import EmbeddingClient, normalize_vector
from .storage import StorageError, _COLUMNS


_VISIBLE = """
    d.current = TRUE AND (d.team = '*' OR d.team = %(team)s)
    AND d.roles ? %(role)s AND d.embedding_space = %(space)s
"""
_SCHEMA = """
CREATE TABLE IF NOT EXISTS index_config (
    id INTEGER PRIMARY KEY CHECK (id = 1), dimensions INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS documents (
    version_id TEXT PRIMARY KEY, document_id TEXT NOT NULL, current BOOLEAN NOT NULL,
    title TEXT NOT NULL, origin TEXT NOT NULL, source_type TEXT NOT NULL, source_url TEXT,
    source_path TEXT NOT NULL, team TEXT NOT NULL, roles JSONB NOT NULL,
    review_by TEXT, rule_key TEXT, rule_value TEXT, embedding_space TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS current_document ON documents(document_id) WHERE current;
CREATE INDEX IF NOT EXISTS document_audience ON documents(team) WHERE current;
CREATE TABLE IF NOT EXISTS chunks (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version_id TEXT NOT NULL REFERENCES documents(version_id), chunk_id TEXT NOT NULL,
    text TEXT NOT NULL, page_number INTEGER, start_char INTEGER NOT NULL, end_char INTEGER NOT NULL,
    extraction_method TEXT NOT NULL, embedding vector({dimensions}) NOT NULL,
    UNIQUE(version_id, chunk_id)
);
CREATE INDEX IF NOT EXISTS chunk_embedding ON chunks USING hnsw (embedding vector_cosine_ops);
CREATE TABLE IF NOT EXISTS access_requests (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee TEXT NOT NULL, project TEXT NOT NULL, permission TEXT NOT NULL,
    reason TEXT NOT NULL, status TEXT NOT NULL, UNIQUE(employee, project, permission)
);
"""


class PgVectorStore:
    def __init__(self, dsn=None, *, embedder=None, min_similarity=None):
        self.dsn = dsn or os.environ.get(
            "RAG_DATABASE_URL", "postgresql://buddy:buddy-local@localhost:55432/buddy")
        self.embedder = embedder if embedder is not None else EmbeddingClient()
        self.embedding_identity = self.embedder.config.identity
        self.dimensions = self.embedder.config.dimensions
        self.min_similarity = (float(os.environ.get("RAG_MIN_SIMILARITY", "0.3"))
                               if min_similarity is None else min_similarity)
        if not math.isfinite(self.min_similarity) or not -1 <= self.min_similarity <= 1:
            raise ValueError("RAG_MIN_SIMILARITY must be between -1 and 1")

    @contextmanager
    def connect(self, *, read_only=False, create=False, timeout_seconds=30):
        try:
            import psycopg
            from psycopg.rows import dict_row
        except ImportError:
            raise StorageError("Install the RAG dependencies including psycopg[binary]") from None
        if timeout_seconds <= 0:
            raise StorageError("Database deadline exceeded")
        try:
            with psycopg.connect(self.dsn, row_factory=dict_row,
                                 connect_timeout=max(1, math.ceil(timeout_seconds)),
                                 options="-c search_path=rag,public") as db:
                if read_only:
                    db.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY")
                db.execute("SELECT set_config('statement_timeout', %s, true)",
                           (str(max(1, int(timeout_seconds * 1000))),))
                if create:
                    db.execute("CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public")
                    db.execute("CREATE SCHEMA IF NOT EXISTS rag")
                    db.execute(_SCHEMA.format(dimensions=self.dimensions))
                    db.execute("INSERT INTO index_config VALUES (1, %s) ON CONFLICT DO NOTHING",
                               (self.dimensions,))
                    if db.execute("SELECT dimensions FROM index_config WHERE id=1").fetchone()["dimensions"] != self.dimensions:
                        raise StorageError("Embedding dimensions changed; use a new database and reindex")
                yield db
        except psycopg.Error:
            # Do not expose DSNs, SQL parameters or source text to transports/logs.
            raise StorageError("PostgreSQL vector index unavailable") from None

    def ready(self):
        if not self.embedder.config.api_key:
            return False
        with self.connect(read_only=True, timeout_seconds=2) as db:
            spaces = db.execute("SELECT DISTINCT embedding_space FROM documents WHERE current").fetchall()
            return (spaces == [{"embedding_space": self.embedding_identity}]
                    and db.execute("SELECT 1 FROM chunks c JOIN documents d USING(version_id) "
                                   "WHERE d.current LIMIT 1").fetchone() is not None)

    def publish(self, documents):
        # Prepare all vectors before publishing: provider failure leaves the old snapshot active.
        with self.connect(create=True) as db:
            existing = {row["version_id"] for row in db.execute("SELECT version_id FROM documents")}
        missing = [(meta, version, chunks) for meta, version, chunks in documents if version not in existing]
        texts = [meta["title"] + "\n\n" + chunk.text
                 for meta, _, chunks in missing for chunk in chunks]
        vectors = self.embedder.embed(texts)
        if len(vectors) != len(texts):
            raise StorageError("Embedding count did not match the chunks")
        vectors = iter([json.dumps(normalize_vector(vector, self.dimensions)) for vector in vectors])
        prepared = {version: [next(vectors) for _ in chunks] for _, version, chunks in missing}
        with self.connect() as db:
            # Serialize manifest publications (including retries from multiple indexers).
            db.execute("SELECT pg_advisory_xact_lock(72419601)")
            db.execute("UPDATE documents SET current = FALSE WHERE current")
            for metadata, version, chunks in documents:
                if db.execute("UPDATE documents SET current = TRUE WHERE version_id = %s", (version,)).rowcount:
                    continue
                db.execute("""
                    INSERT INTO documents(version_id, document_id, current, title, origin, source_type,
                        source_url, source_path, team, roles, review_by, rule_key, rule_value, embedding_space)
                    VALUES(%(version)s, %(id)s, TRUE, %(title)s, %(origin)s, %(source_type)s,
                        %(source_url)s, %(source_path)s, %(team)s, %(roles)s::jsonb, %(review_by)s,
                        %(rule_key)s, %(rule_value)s, %(space)s)
                """, dict(metadata, version=version, roles=json.dumps(metadata["roles"]),
                          space=self.embedding_identity))
                with db.cursor() as cursor:
                    cursor.executemany("""
                        INSERT INTO chunks(version_id, chunk_id, text, page_number, start_char,
                            end_char, extraction_method, embedding)
                        VALUES(%s, %s, %s, %s, %s, %s, %s, %s, %s::vector)
                    """, [(version, chunk.chunk_id, chunk.text, chunk.page_number, chunk.start_char,
                           chunk.end_char, chunk.extraction_method, vector)
                          for chunk, vector in zip(chunks, prepared[version])])

    def search(self, query, audience, top_k, today, *, timeout_seconds=None):
        budget = 30 if timeout_seconds is None else timeout_seconds
        deadline = time.monotonic() + budget
        vector = self.embedder.embed([query], timeout_seconds=budget)[0]
        params = dict(team=audience.team, role=audience.role, space=self.embedding_identity,
                      vector=json.dumps(normalize_vector(vector, self.dimensions)), top_k=top_k,
                      max_distance=1 - self.min_similarity, today=today.isoformat())
        with self.connect(read_only=True, timeout_seconds=deadline - time.monotonic()) as db:
            spaces = db.execute("SELECT DISTINCT embedding_space FROM documents WHERE current").fetchall()
            if spaces and spaces != [{"embedding_space": self.embedding_identity}]:
                raise StorageError("Embedding configuration changed; reindex the documents")
            db.execute("SET LOCAL hnsw.iterative_scan = 'strict_order'")
            selected = db.execute(f"""
                WITH nearest AS MATERIALIZED (
                    SELECT {_COLUMNS}, c.embedding <=> %(vector)s::vector AS distance
                    FROM documents d JOIN chunks c USING(version_id)
                    WHERE {_VISIBLE}
                    ORDER BY c.embedding <=> %(vector)s::vector LIMIT %(top_k)s
                )
                SELECT * FROM nearest WHERE distance <= %(max_distance)s
                ORDER BY distance, document_id, chunk_id
            """, params).fetchall()
            # Contradicting current policies must remain visible even with top_k=1.
            keys = {row["rule_key"] for row in selected if row["rule_key"] and
                    (row["review_by"] is None or row["review_by"] >= params["today"])}
            for key in sorted(keys):
                variants = db.execute(f"""
                    SELECT {_COLUMNS} FROM documents d JOIN chunks c USING(version_id)
                    WHERE {_VISIBLE} AND d.rule_key = %(key)s
                      AND (d.review_by IS NULL OR d.review_by >= %(today)s)
                    ORDER BY d.document_id, c.id
                """, dict(params, key=key)).fetchall()
                if len({row["rule_value"] for row in variants}) > 1:
                    seen = {row["version_id"] for row in selected}
                    for row in variants:
                        if row["version_id"] not in seen:
                            selected.append(row)
                            seen.add(row["version_id"])
            return selected

    @contextmanager
    def access_transaction(self, *, confirm):
        with self.connect(read_only=not confirm) as db:
            if confirm:
                db.execute("SELECT pg_advisory_xact_lock(72419601)")
            yield db

    @staticmethod
    def access_rules(db, audience, today):
        return db.execute("""
            SELECT document_id, rule_value FROM documents
            WHERE current AND (team = '*' OR team = %s) AND roles ? %s
              AND rule_key = 'jira.bud.read' AND (review_by IS NULL OR review_by >= %s)
        """, (audience.team, audience.role, today.isoformat())).fetchall()

    @staticmethod
    def access_receipt(db, employee, fields, *, confirm):
        params = dict(fields, employee=employee)
        if confirm:
            db.execute("""
                INSERT INTO access_requests(employee, project, permission, reason, status)
                VALUES(%(employee)s, %(project)s, %(permission)s, %(reason)s, 'pending')
                ON CONFLICT(employee, project, permission) DO NOTHING
            """, params)
        return db.execute("""
            SELECT id, status, reason FROM access_requests
            WHERE employee = %(employee)s AND project = %(project)s AND permission = %(permission)s
        """, params).fetchone()
