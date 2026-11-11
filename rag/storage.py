"""A local FTS5 index. Each manifest is published as one SQLite transaction."""

import json
import os
import sqlite3
from contextlib import closing, contextmanager
from dataclasses import dataclass
from datetime import date
from pathlib import Path

from .terms import search_terms


ROLES = ("intern", "mentor", "lead")
SOURCE_TYPES = ("handbook", "team_guide", "checklist", "runbook")

_SCHEMA = """
CREATE TABLE IF NOT EXISTS documents (
    version_id TEXT PRIMARY KEY, document_id TEXT NOT NULL, current INTEGER NOT NULL,
    title TEXT NOT NULL, origin TEXT NOT NULL, source_type TEXT NOT NULL, source_url TEXT,
    source_path TEXT NOT NULL, team TEXT NOT NULL,
    roles TEXT NOT NULL, review_by TEXT, rule_key TEXT, rule_value TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS current_document ON documents(document_id) WHERE current = 1;
CREATE TABLE IF NOT EXISTS chunks (
    id INTEGER PRIMARY KEY, version_id TEXT NOT NULL REFERENCES documents(version_id),
    chunk_id TEXT NOT NULL, text TEXT NOT NULL, page_number INTEGER, start_char INTEGER NOT NULL,
    end_char INTEGER NOT NULL, extraction_method TEXT NOT NULL, UNIQUE(version_id, chunk_id)
);
CREATE TABLE IF NOT EXISTS access_requests (
    id INTEGER PRIMARY KEY, employee TEXT NOT NULL, project TEXT NOT NULL, permission TEXT NOT NULL,
    reason TEXT NOT NULL, status TEXT NOT NULL, UNIQUE(employee, project, permission)
);
CREATE VIRTUAL TABLE IF NOT EXISTS passages USING fts5(title_terms, body_terms);
"""
_VISIBLE = """
    d.current = 1 AND (d.team = '*' OR d.team = :team)
    AND EXISTS (SELECT 1 FROM json_each(d.roles) WHERE value = :role)
"""
_COLUMNS = """
    d.version_id, d.document_id, d.title, d.source_type, d.source_url, d.source_path, d.team, d.origin,
    d.review_by, d.rule_key, d.rule_value, d.roles, c.chunk_id, c.text, c.page_number,
    c.start_char, c.end_char, c.extraction_method
"""


@dataclass(frozen=True)
class Audience:
    role: str
    team: str

    def __post_init__(self):
        if self.role not in ROLES or not isinstance(self.team, str) or not self.team.strip() or self.team == "*":
            raise ValueError("Supply an employee role and a concrete team")


class SQLiteStore:
    def __init__(self, path=None):
        self.path = Path(path or os.environ.get("RAG_DB_PATH", "ai-buddy.sqlite3"))

    @contextmanager
    def connect(self, *, read_only=False, create=False):
        mode = "ro" if read_only else ("rwc" if create else "rw")
        target = self.path.resolve().as_uri() + "?mode=" + mode
        with closing(sqlite3.connect(target, uri=True, timeout=10)) as db:
            db.row_factory = sqlite3.Row
            db.execute("PRAGMA foreign_keys = ON")
            with db:
                yield db

    def publish(self, documents):
        """Publish a complete snapshot, reusing an existing immutable version on retries."""
        with self.connect(create=True) as db:
            db.executescript(_SCHEMA)
            db.execute("BEGIN IMMEDIATE")
            db.execute("UPDATE documents SET current = 0 WHERE current = 1")
            for metadata, version, chunks in documents:
                if db.execute("UPDATE documents SET current = 1 WHERE version_id = ?", (version,)).rowcount:
                    continue
                db.execute("""
                    INSERT INTO documents(version_id, document_id, current, title, origin, source_type,
                        source_url, source_path, team, roles, review_by, rule_key, rule_value)
                    VALUES(:version, :id, 1, :title, :origin, :source_type, :source_url, :source_path, :team,
                           :roles, :review_by, :rule_key, :rule_value)
                """, dict(metadata, version=version, roles=json.dumps(metadata["roles"])))
                title_terms = " ".join(search_terms(metadata["title"]))
                for chunk in chunks:
                    row = db.execute("""
                        INSERT INTO chunks(version_id, chunk_id, text, page_number, start_char,
                            end_char, extraction_method) VALUES (?, ?, ?, ?, ?, ?, ?)
                    """, (version, chunk.chunk_id, chunk.text, chunk.page_number,
                          chunk.start_char, chunk.end_char, chunk.extraction_method))
                    db.execute("INSERT INTO passages(rowid, title_terms, body_terms) VALUES(?, ?, ?)",
                               (row.lastrowid, title_terms, " ".join(search_terms(chunk.text))))

    def search(self, terms, audience, top_k, today: date):
        # Tokens originate from the tokenizer, never from raw FTS query syntax.
        match = " OR ".join(f'"{term}"' for term in terms)
        params = dict(role=audience.role, team=audience.team, match=match, today=today.isoformat())
        with self.connect(read_only=True) as db:
            db.execute("BEGIN")  # both the ranked search and rule expansion see one ACL snapshot
            rows = db.execute(f"""
                SELECT {_COLUMNS}, bm25(passages, 3.0, 1.0) AS rank,
                       title_terms, body_terms FROM passages
                JOIN chunks c ON c.id = passages.rowid JOIN documents d USING(version_id)
                WHERE {_VISIBLE} AND passages MATCH :match ORDER BY rank, d.document_id, c.id
            """, params)
            # Avoid claiming a match for "стоимость лицензии Jira" on "Jira" alone.
            wanted = set(terms)
            selected = []
            best_overlap = len(wanted) // 2 + 1
            for row in rows:
                available = set((row["title_terms"] + " " + row["body_terms"]).split())
                overlap = len(wanted & available)
                if overlap > best_overlap:
                    selected.clear()
                    best_overlap = overlap
                if overlap == best_overlap and len(selected) < top_k:
                    selected.append(row)
            # A generic rule and a team exception can disagree even if only one
            # matched the wording or survived top_k. Include both original sources.
            keys = {row["rule_key"] for row in selected if row["rule_key"] and
                    (row["review_by"] is None or row["review_by"] >= params["today"])}
            for key in sorted(keys):
                variants = db.execute(f"""
                    SELECT {_COLUMNS} FROM documents d JOIN chunks c USING(version_id)
                    WHERE {_VISIBLE} AND d.rule_key = :key
                      AND (d.review_by IS NULL OR d.review_by >= :today)
                    ORDER BY d.document_id, c.id
                """, dict(params, key=key)).fetchall()
                if len({row["rule_value"] for row in variants}) > 1:
                    seen = {row["version_id"] for row in selected}
                    for row in variants:
                        if row["version_id"] not in seen:
                            selected.append(row)
                            seen.add(row["version_id"])
            return selected
