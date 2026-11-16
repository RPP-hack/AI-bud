"""Unit contracts plus opt-in tests against a real, disposable pgvector database."""

from contextlib import contextmanager
from dataclasses import asdict, replace
from datetime import date
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch
import uuid

from rag.access import PROFILES, request_access
from rag.application import Actor, ApplicationError, RagApplication
from rag.embeddings import EmbeddingConfig, EmbeddingError
from rag.ingestion.indexing import index_manifest
from rag.ingestion.chunking import Chunk
from rag.pipeline import ask
from rag.retrieval import RetrievedChunk, Retriever
from rag.storage import StorageError, create_store
from rag.vector_storage import PgVectorStore


INTERN = PROFILES["intern-platform"]


def passage(**changes):
    fields = dict(version_id="version", document_id="jira-access", chunk_id="chunk",
                  text="Подай заявку на чтение BUD.", source_type="runbook", source_path="jira.md",
                  origin="demo", title="Доступ к трекеру", source_url=None, team="platform",
                  roles=("intern",), review_by=None, stale=False, rule_key="jira.bud.read",
                  rule_value="Подать заявку на чтение BUD", page_number=None,
                  start_char=0, end_char=28, extraction_method="text")
    return RetrievedChunk(**dict(fields, **changes))


class VectorStorageTests(unittest.TestCase):
    def test_indexing_without_a_key_fails_before_database_access_even_for_cached_vectors(self):
        embedder = Mock(config=EmbeddingConfig())
        store = PgVectorStore(embedder=embedder)
        with patch.object(store, "connect") as connect:
            with self.assertRaisesRegex(EmbeddingError, "RAG_EMBEDDING_API_KEY or RAG_LLM_API_KEY"):
                store.publish([])
        connect.assert_not_called()
        embedder.embed.assert_not_called()

    def test_defaults_use_pgvector_even_with_legacy_sqlite_environment(self):
        with patch.dict(os.environ, {"RAG_DB_PATH": "legacy.sqlite3"}, clear=True):
            self.assertIsInstance(create_store(), PgVectorStore)
            self.assertIsInstance(Retriever().store, PgVectorStore)
            self.assertIsInstance(RagApplication().store, PgVectorStore)

    def test_raw_paraphrase_is_sent_to_storage_and_actions_use_evidence(self):
        query = "Меня не пускает в трекер, помоги разобраться"
        row = asdict(passage())
        row.pop("stale")
        row["roles"] = ["intern"]  # PostgreSQL JSONB is already decoded.
        row["distance"] = 0.15
        store = Mock()
        store.search.return_value = [row]
        result = ask(query, audience=INTERN, store=store, offline=True, top_k=1)
        self.assertEqual(result.action, "preview_jira_access")
        self.assertEqual(store.search.call_args.args[0], query)
        self.assertEqual(result.sources["S1"], passage())
        unrelated = dict(row, document_id="first-pr", version_id="other", rule_key=None, rule_value=None)
        store.search.return_value = [unrelated, row]
        self.assertIsNone(ask("Как начать работу с кодом?", audience=INTERN, store=store, offline=True).action)
        store.search.return_value = [row]
        row["review_by"] = "2000-01-01"
        self.assertIsNone(ask(query, audience=INTERN, store=store, offline=True).action)

    def test_dependency_failures_become_public_application_errors(self):
        for error in (EmbeddingError("provider details"), StorageError("database details")):
            store = Mock()
            store.search.side_effect = error
            app = RagApplication(store)
            with self.subTest(error=type(error)), self.assertRaisesRegex(ApplicationError, "^temporarily_unavailable$"):
                app.answer("Вопрос", Actor("u", "intern", "platform"))
        store.ready.side_effect = StorageError("database details")
        self.assertFalse(RagApplication(store).ready())

    def test_failed_embedding_does_not_start_snapshot_publication(self):
        embedder = Mock(config=EmbeddingConfig("test", dimensions=3))
        embedder.embed.side_effect = EmbeddingError("provider down")
        store = PgVectorStore(embedder=embedder)
        db = Mock()
        db.execute.return_value = []

        @contextmanager
        def connect(**kwargs):
            yield db

        with patch.object(store, "connect", side_effect=connect) as connection:
            with tempfile.TemporaryDirectory() as root:
                path = Path(root)
                (path / "doc.md").write_text("Инструкция", encoding="utf-8")
                (path / "manifest.json").write_text(json.dumps([dict(
                    id="doc", path="doc.md", title="Заголовок", source_type="runbook",
                    origin="demo", team="platform", roles=["intern"])]), encoding="utf-8")
                with self.assertRaises(EmbeddingError):
                    index_manifest(path / "manifest.json", store)
        self.assertEqual(connection.call_count, 1)
        self.assertEqual(embedder.embed.call_args.args[0], ["Заголовок\n\nИнструкция"])
        self.assertFalse(any("UPDATE" in call.args[0] for call in db.execute.call_args_list))

    def test_pg_search_embeds_raw_query_and_expands_conflicting_evidence(self):
        embedder = Mock(config=EmbeddingConfig("test", dimensions=3))
        embedder.embed.return_value = [[1, 0, 0]]
        store = PgVectorStore(embedder=embedder, min_similarity=0.7)
        row = asdict(passage())
        row.pop("stale")
        other = dict(row, version_id="other", document_id="other", rule_value="Другой порядок")
        db = Mock()
        db.execute.side_effect = [
            Mock(fetchall=lambda: [{"embedding_space": store.embedding_identity}]),
            Mock(), Mock(fetchall=lambda: [row]), Mock(fetchall=lambda: [row, other]),
        ]

        @contextmanager
        def connect(**kwargs):
            self.assertTrue(kwargs["read_only"])
            self.assertGreater(kwargs["timeout_seconds"], 0)
            self.assertLessEqual(kwargs["timeout_seconds"], 2)
            yield db

        with patch.object(store, "connect", side_effect=connect):
            selected = store.search("Как мне начать работу?", INTERN, 1, date(2026, 11, 16), timeout_seconds=2)
        self.assertEqual([r["document_id"] for r in selected], ["jira-access", "other"])
        embedder.embed.assert_called_once_with(["Как мне начать работу?"], timeout_seconds=2)
        sql, params = db.execute.call_args_list[2].args
        self.assertIn("d.roles ? %(role)s", sql)
        self.assertLess(sql.index("WHERE"), sql.index("LIMIT"))
        self.assertEqual(params["top_k"], 1)
        self.assertAlmostEqual(params["max_distance"], 0.3)
        self.assertEqual(params["team"], "platform")

    def test_pg_publish_preserves_text_and_stores_normalized_vectors(self):
        embedder = Mock(config=EmbeddingConfig("test", dimensions=3))
        embedder.embed.return_value = [[3, 0, 0]]
        store = PgVectorStore(embedder=embedder)
        db = Mock()
        db.execute.side_effect = [[], Mock(), Mock(), Mock(rowcount=0), Mock()]
        cursor = Mock()
        db.cursor.return_value.__enter__ = Mock(return_value=cursor)
        db.cursor.return_value.__exit__ = Mock(return_value=False)
        chunk = Chunk("chunk", "Текст фрагмента", 2, 10, 24, "pdf-text")
        metadata = dict(id="doc", title="Заголовок", origin="demo", source_type="runbook",
                        source_url=None, source_path="doc.pdf", team="platform", roles=["intern"],
                        review_by=None, rule_key=None, rule_value=None)

        @contextmanager
        def connect(**kwargs):
            yield db

        with patch.object(store, "connect", side_effect=connect):
            store.publish([(metadata, "version", [chunk])])
        embedder.embed.assert_called_once_with(["Заголовок\n\nТекст фрагмента"])
        values = cursor.executemany.call_args.args[1][0]
        self.assertEqual(values[:-1], ("version", "chunk", chunk.text, 2, 10, 24, "pdf-text"))
        self.assertEqual(json.loads(values[-1]), [1, 0, 0])


@unittest.skipUnless(os.environ.get("RAG_TEST_DATABASE_URL"), "Set RAG_TEST_DATABASE_URL for pgvector integration")
class PgVectorIntegrationTests(unittest.TestCase):
    """The supplied admin DSN needs CREATEDB; each test uses a new temporary database."""

    def setUp(self):
        import psycopg
        from psycopg import sql
        from psycopg.conninfo import make_conninfo
        admin_dsn = os.environ["RAG_TEST_DATABASE_URL"]
        name = "rag_test_" + uuid.uuid4().hex
        admin = psycopg.connect(admin_dsn, autocommit=True)
        self.addCleanup(admin.close)
        admin.execute(sql.SQL("CREATE DATABASE {}").format(sql.Identifier(name)))
        self.addCleanup(lambda: admin.execute(sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(name))))
        self.embedder = Mock(config=EmbeddingConfig("test", dimensions=3))
        self.embedder.embed.side_effect = lambda texts, **kwargs: [[1, 0, 0] for _ in texts]
        self.store = PgVectorStore(make_conninfo(admin_dsn, dbname=name),
                                   embedder=self.embedder, min_similarity=0.9)
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)

    def index(self, entries):
        manifest = []
        for entry in entries:
            entry = dict(entry)
            text = entry.pop("text", "Инструкция к документу.")
            entry = dict({"title": "Инструкция", "source_type": "runbook", "origin": "demo",
                          "team": "platform", "roles": ["intern"]}, **entry)
            entry["path"] = entry["id"] + ".md"
            (self.root / entry["path"]).write_text(text, encoding="utf-8")
            manifest.append(entry)
        path = self.root / "manifest.json"
        path.write_text(json.dumps(manifest), encoding="utf-8")
        return index_manifest(path, self.store)

    def search(self, query="Произвольная переформулировка", **kwargs):
        return Retriever(self.store).retrieve(query, audience=INTERN, **kwargs)

    def test_cosine_acl_before_top_k_threshold_and_citation_offsets(self):
        self.index([dict(id="hidden-team", team="search"),
                    dict(id="hidden-role", roles=["mentor"]), dict(id="visible", text="Текст инструкции.")])
        hits = self.search(top_k=1)
        self.assertEqual([p.document_id for p in hits], ["visible"])
        self.assertEqual(hits[0].text, (self.root / "visible.md").read_text()[hits[0].start_char:hits[0].end_char])
        self.embedder.embed.side_effect = lambda texts, **kwargs: [[0, 1, 0] for _ in texts]
        self.assertEqual(self.search(), [])

    def test_retries_reuse_vectors_and_manifest_replacement_revokes_documents(self):
        entries = [dict(id="visible"), dict(id="removed")]
        self.index(entries)
        original = {p.document_id: p.citation_id for p in self.search()}
        self.embedder.embed.reset_mock()
        self.index(entries)
        self.embedder.embed.assert_called_once_with([])
        self.assertEqual(original, {p.document_id: p.citation_id for p in self.search()})
        self.index([dict(id="visible", roles=["mentor"])])
        self.assertEqual(self.search(), [])
        self.index(entries)
        self.assertEqual(original, {p.document_id: p.citation_id for p in self.search()})

    def test_failed_provider_and_failed_sql_keep_previous_snapshot(self):
        self.index([dict(id="old")])
        self.embedder.embed.side_effect = EmbeddingError("unavailable")
        with self.assertRaises(EmbeddingError):
            self.index([dict(id="new")])
        self.embedder.embed.side_effect = lambda texts, **kwargs: [[1, 0, 0] for _ in texts]
        with self.store.connect() as db:
            db.execute("ALTER TABLE documents ADD CONSTRAINT reject_new CHECK (document_id <> 'new')")
        with self.assertRaises(StorageError):
            self.index([dict(id="new")])
        self.assertEqual([p.document_id for p in self.search()], ["old"])

    def test_conflicts_ignore_top_k_and_expired_variants(self):
        self.index([dict(id="policy-a", rule_key="review", rule_value="Наставник"),
                    dict(id="policy-b", rule_key="review", rule_value="Дежурный"),
                    dict(id="expired", review_by="2000-01-01", rule_key="review", rule_value="Лид")])
        # Give the expired chunk lower similarity so it is not the initial hit.
        with self.store.connect() as db:
            db.execute("UPDATE chunks SET embedding = '[0,1,0]' WHERE version_id IN "
                       "(SELECT version_id FROM documents WHERE document_id = 'expired')")
        result = ask("К кому обратиться?", audience=INTERN, store=self.store, offline=True, top_k=1)
        self.assertEqual(result.status, "conflict")
        self.assertEqual({p.document_id for p in result.sources.values()}, {"policy-a", "policy-b"})

    def test_model_change_requires_reindex_and_dimension_mismatch_is_rejected(self):
        self.index([dict(id="doc")])
        self.assertTrue(self.store.ready())
        config = self.embedder.config
        self.embedder.config = replace(config, model="another-model")
        other = PgVectorStore(self.store.dsn, embedder=self.embedder)
        self.assertFalse(other.ready())
        with self.assertRaises(StorageError):
            Retriever(other).retrieve("вопрос", audience=INTERN)
        with patch.object(self, "store", other):
            self.index([dict(id="doc")])
        self.assertTrue(other.ready())
        self.embedder.config = replace(config, dimensions=2)
        with self.assertRaises(StorageError):
            with PgVectorStore(self.store.dsn, embedder=self.embedder).connect(create=True):
                pass

    def test_access_preview_confirmation_and_idempotency(self):
        self.index([dict(id="jira-access", rule_key="jira.bud.read", rule_value="Подать заявку на чтение BUD")])
        self.assertEqual(request_access(self.store, "intern-platform")["status"], "draft")
        receipt = request_access(self.store, "intern-platform", confirm=True)
        self.assertEqual(receipt["status"], "pending")
        self.assertEqual(request_access(self.store, "intern-platform", confirm=True), receipt)
        self.index([])
        with self.assertRaises(PermissionError):
            request_access(self.store, "intern-platform", confirm=True)


if __name__ == "__main__":
    unittest.main()
