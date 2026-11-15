import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from rag.application import Actor, ApplicationError, RagApplication
from rag.generation import GenerationConfig, GenerationError, Generator
from rag.ingestion.indexing import DEMO_MANIFEST, index_manifest
from rag.storage import SQLiteStore


class ApplicationTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.store = SQLiteStore(Path(temp.name) / "index.db")
        index_manifest(DEMO_MANIFEST, self.store)
        self.app = RagApplication(self.store)
        self.actor = Actor("employee-42", "intern", "platform")

    def test_offline_projection_and_no_side_effects(self):
        result = self.app.answer("Jira 403", self.actor, request_id="test-123")
        self.assertEqual(result["request_id"], "test-123")
        self.assertEqual(result["action"], "preview_jira_access")
        self.assertEqual(result["status"], "sources_found")
        self.assertIsNone(result["model"])
        for source in result["sources"].values():
            self.assertNotIn("source_path", source)
            self.assertNotIn("roles", source)
            self.assertIn("start_char", source)
        with self.store.connect(read_only=True) as db:
            self.assertEqual(db.execute("SELECT COUNT(*) FROM access_requests").fetchone()[0], 0)

    def test_validation_and_readiness(self):
        self.assertTrue(self.app.ready())
        for args in ({"question": ""}, {"question": "x" * 4001}, {"question": "x\x00"},
                     {"question": "\ud800"}, {"top_k": 0}, {"top_k": True}, {"top_k": 51},
                     {"mode": "unknown"}, {"request_id": "bad\nID"},
                     {"actor": Actor("", "intern", "platform")},
                     {"actor": Actor("u", "admin", "platform")},
                     {"actor": Actor("u", "intern", "*")}):
            kwargs = dict(question="Jira 403", actor=self.actor)
            kwargs.update(args)
            with self.subTest(args=args), self.assertRaisesRegex(ApplicationError, "invalid_input"):
                self.app.answer(**kwargs)
        missing = RagApplication(SQLiteStore(self.store.path.parent / "missing.db"))
        self.assertFalse(missing.ready())
        with self.assertRaisesRegex(ApplicationError, "temporarily_unavailable"):
            missing.answer("Jira 403", self.actor)

    def test_acl_and_business_outcomes(self):
        for query in ("MENTOR-482", "SEARCH-730"):
            self.assertEqual(self.app.answer(query, self.actor)["status"], "insufficient_evidence")
            self.assertEqual(self.app.search(query, self.actor)["sources"], [])
        self.assertTrue(self.app.search("MENTOR-482", Actor("mentor", "mentor", "platform"))["sources"])
        self.assertEqual(self.app.answer("Кого позвать на ревью?", self.actor, top_k=1)["status"], "conflict")
        self.assertEqual(self.app.answer("dev-окружение", self.actor)["status"], "needs_review")

    def test_capacity_and_expired_budget(self):
        app = RagApplication(self.store, concurrency=1)
        with app._attempt(1):
            with self.assertRaisesRegex(ApplicationError, "resource_exhausted"):
                app.answer("Jira 403", self.actor)
        with self.assertRaisesRegex(ApplicationError, "deadline_exceeded"):
            app.answer("Jira 403", self.actor, timeout_seconds=0)
        self.assertEqual(app.answer("Jira 403", self.actor)["status"], "sources_found")

    def test_online_generation_uses_remaining_budget_and_validates_citations(self):
        budgets = []
        class Client:
            def complete(self, messages):
                return {"choices": [{"finish_reason": "stop", "message": {"content": json.dumps({
                    "status": "answered", "steps": [{"text": "Request read access", "source_ids": ["S1"]}]})}}]}
        def factory(timeout):
            budgets.append(timeout)
            return Generator(GenerationConfig("test"), client=Client())
        app = RagApplication(self.store, generator_factory=factory)
        result = app.answer("Jira 403", self.actor, mode="llm", timeout_seconds=2)
        self.assertEqual(result["status"], "answered")
        self.assertTrue(0 < budgets[0] <= 2)
        with patch.object(Client, "complete", side_effect=GenerationError("private provider details")):
            with self.assertRaisesRegex(ApplicationError, "^temporarily_unavailable$"):
                app.answer("Jira 403", self.actor, mode="llm")
        with patch.dict("os.environ", {"RAG_LLM_API_KEY": ""}):
            with self.assertRaisesRegex(ApplicationError, "temporarily_unavailable"):
                self.app.answer("Jira 403", self.actor, mode="llm")
            self.assertEqual(self.app.answer("no matching evidence", self.actor, mode="llm")["status"], "insufficient_evidence")
