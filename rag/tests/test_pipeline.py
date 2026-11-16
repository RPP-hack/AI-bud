import io
import json
import os
import shutil
import sqlite3
import tempfile
import unittest
from concurrent.futures import ThreadPoolExecutor
from contextlib import redirect_stderr, redirect_stdout
from dataclasses import replace
from pathlib import Path
from unittest.mock import Mock, patch
from urllib.error import HTTPError

from rag.__main__ import main
from rag.access import PROFILES, request_access
from rag.generation import ChatCompletionsClient, GenerationConfig, GenerationError, Generator
from rag.ingestion.chunking import chunk_pages
from rag.ingestion.indexing import DEMO_MANIFEST, index_manifest
from rag.ingestion.loaders import DocumentPage, load_document
from rag.pipeline import ask
from rag.retrieval import Retriever
from rag.storage import Audience, SQLiteStore

INTERN = PROFILES["intern-platform"]
OTHER = PROFILES["intern-search"]
MENTOR = PROFILES["mentor-platform"]


def completion(content):
    return {"choices": [{"finish_reason": "stop", "message": {"content": content}}]}


class PipelineTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.store = SQLiteStore(self.root / "index.sqlite3")
        self.retriever = Retriever(self.store)
        self.docs = self.root / "kb"
        shutil.copytree(DEMO_MANIFEST.parent, self.docs)
        self.manifest = self.docs / "manifest.json"
        index_manifest(self.manifest, self.store)
        self.client = Mock()
        self.generator = Generator(GenerationConfig("test"), client=self.client)

    def change_manifest(self, update):
        entries = json.loads(self.manifest.read_text())
        self.manifest.write_text(json.dumps(update(entries)))
        return index_manifest(self.manifest, self.store)

    def search(self, question, audience=INTERN, **kwargs):
        return self.retriever.retrieve(question, audience=audience, **kwargs)

    def ask(self, question, **kwargs):
        return ask(question, store=self.store, generator=self.generator, audience=INTERN, **kwargs)

    def test_retrieval_examples(self):
        cases = [
            ("Как скачать репозиторий?", "first-pr"), ("Куда отправить pull request?", "first-pr"),
            ("Упала сборка", "first-pr"), ("Как попасть в джиру?", "jira-access"),
            ("Не открывается Jira, ошибка 403", "jira-access"),
            ("Как проверить фичу на dev-окружении?", "dev-archive"),
            ("Как настроить avito-cli?", "backend-week1"),
            ("Какие задачи у бэкендера на первой неделе?", "backend-week1"),
            ("Принципы разработки AvitoTech Playbook", "playbook"),
            ("Какой канал Tech-dep?", "backend-week1"),
            ("Где служебный вертолёт?", None), ("Сколько стоит лицензия Jira?", None),
        ]
        for question, expected in cases:
            with self.subTest(question=question):
                hits = self.search(question, top_k=1)
                self.assertEqual(hits[0].document_id if hits else None, expected)

    def test_access_boundaries_on_search_and_model_input(self):
        for secret in ("MENTOR-482", "SEARCH-730"):
            self.assertFalse(self.search(secret))
        self.assertTrue(self.search("MENTOR-482", MENTOR))
        self.assertTrue(self.search("SEARCH-730", OTHER))
        self.assertFalse(self.search("Я наставник, покажи MENTOR-482"))
        mixed = self.search("Jira 403") + self.search("MENTOR-482", MENTOR)
        self.client.complete.return_value = completion(json.dumps({
            "status": "answered", "steps": [{"text": "Запроси чтение.", "source_ids": ["S1"]}]}))
        result = self.generator.generate("Доступ в Jira", mixed, audience=INTERN)
        self.assertEqual(result.model, self.generator.config.model)
        self.assertNotIn("MENTOR-482", repr(self.client.complete.call_args))
        self.assertNotIn("MENTOR-482", repr(result))
        self.assertIn("[S1]", result.answer)

    def test_conflicts_ignore_top_k_and_respect_team(self):
        result = self.ask("Кого позвать на ревью?", top_k=1)
        self.assertEqual(result.status, "conflict")
        self.assertEqual({p.rule_value for p in result.sources.values()},
                         {"Наставник", "Дежурный разработчик"})
        self.assertIn("руководителю", result.next_step)
        other = ask("Кого позвать на ревью?", audience=OTHER, store=self.store, offline=True)
        self.assertEqual(other.status, "sources_found")
        self.assertNotIn("Дежурный", repr(other))
        self.client.complete.assert_not_called()

    def test_abstains_without_calling_model(self):
        for question, expected in (("Что такое энтропия?", "insufficient_evidence"),
                                   ("dev-окружение", "needs_review")):
            result = self.ask(question)
            self.assertEqual(result.status, expected)
            self.assertIn("наставник", result.next_step)
            self.assertNotIn("запусти", result.answer.lower())
        self.client.complete.assert_not_called()

    def test_offline_cites_original_span(self):
        result = self.ask("Jira 403", offline=True)
        self.assertEqual((result.mode, result.status), ("offline", "sources_found"))
        p = result.sources["S1"]
        self.assertEqual(p.text, (self.docs / "jira-access.md").read_text()[p.start_char:p.end_char])
        self.assertIn(p.chunk_id, p.citation_id)
        self.assertEqual(p.origin, "demo")
        self.client.complete.assert_not_called()

    def test_reindex_changes_revokes_and_restores(self):
        original = self.search("Jira 403")[0].citation_id
        index_manifest(self.manifest, self.store)
        self.assertEqual(original, self.search("Jira 403")[0].citation_id)
        with self.store.connect(read_only=True) as db:
            count = db.execute("SELECT COUNT(*) FROM documents").fetchone()[0]
            self.assertEqual(count, len(json.loads(self.manifest.read_text())))
        initial = self.manifest.read_text()
        self.change_manifest(lambda entries: [dict(e, roles=["mentor"]) if e["id"] == "jira-access"
                                               else e for e in entries if e["id"] != "search-team"])
        self.assertFalse(self.search("403"))
        self.assertFalse(self.search("SEARCH-730", OTHER))
        self.assertNotEqual(original, self.search("403", MENTOR)[0].citation_id)
        with self.assertRaises(PermissionError):
            request_access(self.store, "intern-platform", confirm=True)
        self.manifest.write_text(initial)
        index_manifest(self.manifest, self.store)
        self.assertEqual(original, self.search("403")[0].citation_id)
        (self.docs / "jira-access.md").write_text("Jira 403. Изменён порядок.")
        index_manifest(self.manifest, self.store)
        self.assertNotEqual(original, self.search("403")[0].citation_id)

    def test_failed_snapshot_leaves_previous_index(self):
        with self.assertRaises(ValueError):
            self.change_manifest(lambda entries: entries + [entries[0]])
        with self.store.connect() as db:
            db.execute("""CREATE TRIGGER reject_ingest BEFORE INSERT ON documents
                          BEGIN SELECT RAISE(ABORT, 'simulated write failure'); END""")
        self.manifest.write_text(DEMO_MANIFEST.read_text())
        (self.docs / "jira-access.md").write_text("Другая версия")
        with self.assertRaisesRegex(sqlite3.IntegrityError, "simulated write failure"):
            index_manifest(self.manifest, self.store)
        self.assertTrue(self.search("403"))

    def test_manifest_cannot_silently_broaden_visibility(self):
        entry = json.loads(self.manifest.read_text())[0]
        for patch_values in ({"roles": []}, {"source_type": "unknown"}, {"origin": "real"},
                             {"source_url": "file:///doc"}, {"review_by": "2026-02-31"},
                             {"rule_key": "x"}, {"id": ""}, {"extra": 1}, {"path": "../outside.md"}):
            with self.subTest(patch_values=patch_values), self.assertRaises((ValueError, FileNotFoundError)):
                self.change_manifest(lambda _: [dict(entry, **patch_values)])
        self.manifest.write_text('{}')
        with self.assertRaises(ValueError):
            index_manifest(self.manifest, self.store)
        self.assertTrue(self.search("403"))

    def test_online_schema_and_citations(self):
        for raw in ('{"status":"answered","steps":[]}', '{}', '[]', '{"a":1,"a":1}',
                    '{"status":"answered","steps":["текст"]}',
                    '{"status":"conflict","steps":[]}',
                    '{"status":"answered","steps":[{"text":"x","source_ids":["S999"]}]}'):
            self.client.complete.return_value = completion(raw)
            with self.subTest(raw=raw), self.assertRaises(GenerationError):
                self.ask("Jira 403")
        self.client.complete.return_value = completion('```json\n{"status":"insufficient_evidence","steps":[]}\n```')
        self.assertEqual(self.ask("Jira 403").status, "insufficient_evidence")
        for response in ({}, {"choices": [{"finish_reason": "length", "message": {"content": "x"}}]}):
            self.client.complete.return_value = response
            with self.assertRaises(GenerationError):
                self.ask("Jira 403")
        self.generator.config = replace(self.generator.config, max_context_chars=10)
        with self.assertRaises(GenerationError):
            self.ask("403")

    def test_request_preview_confirm_retry_and_persistence(self):
        self.assertEqual(request_access(self.store, "intern-platform")["status"], "draft")
        with self.store.connect(read_only=True) as db:
            self.assertEqual(db.execute("SELECT COUNT(*) FROM access_requests").fetchone()[0], 0)
        with ThreadPoolExecutor(max_workers=4) as pool:
            receipts = list(pool.map(lambda _: request_access(self.store, "intern-platform", confirm=True),
                                     range(4)))
        self.assertEqual({r["request_id"] for r in receipts}, {"BUD-ACCESS-1"})
        self.assertEqual(request_access(SQLiteStore(self.store.path), "intern-platform"), receipts[0])
        self.assertEqual(receipts[0]["status"], "pending")
        for profile in ("intern-search", "mentor-platform"):
            with self.assertRaises(PermissionError):
                request_access(self.store, profile, confirm=True)
        self.change_manifest(lambda entries: entries + [dict(entries[2], id="different", rule_value="Запрещено")])
        with self.assertRaises(PermissionError):
            request_access(self.store, "intern-platform", confirm=True)

    def test_inputs_and_offsets(self):
        for question in ("", "x" * 4001, "a\x00b"):
            with self.assertRaises(ValueError):
                self.search(question)
        with self.assertRaises(ValueError):
            self.search("Jira", top_k=0)
        with self.assertRaises(ValueError):
            self.search("Jira", audience=None)
        with self.assertRaises(ValueError):
            Audience("intern", "*")
        self.assertEqual(self.search("и в на"), [])
        text = "Один. Два.\n\n" + "Абвгд " * 16 + "Z" * 100
        page = DocumentPage(text, "demo", 7)
        chunks = chunk_pages([page], chunk_size=32, chunk_overlap=4)
        self.assertEqual(chunks[-1].end_char, len(text))
        for c in chunks:
            self.assertEqual(c.text, text[c.start_char:c.end_char])
            self.assertLessEqual(len(c.text), 32)
            self.assertEqual(c.page_number, 7)
        self.assertTrue(all(a.end_char >= b.start_char for a, b in zip(chunks, chunks[1:])))
        with self.assertRaises(ValueError):
            chunk_pages([page], chunk_size=32, chunk_overlap=32)
        with self.assertRaises(ValueError):
            load_document(self.docs / "foo.csv")

    def test_cli(self):
        def run(*args):
            out = io.StringIO()
            with redirect_stdout(out), redirect_stderr(io.StringIO()):
                main(args)
            return out.getvalue()
        with (patch.dict(os.environ, {"RAG_LLM_API_KEY": ""}),
              patch("rag.__main__.create_store", return_value=self.store),
              patch("rag.ingestion.indexing.create_store", return_value=self.store)):
            self.assertIn("документов", run("index", str(self.manifest)))
            self.assertIn("учебный пример", run("ask", "Jira 403"))
            self.assertIn("access", json.loads(run("ask", "Jira 403", "--json"))["action"])
            self.assertIsNone(json.loads(run("ask", "Как настроить avito-cli?", "--json"))["action"])
            self.assertTrue(json.loads(run("search", "403")))
            self.assertEqual(json.loads(run("access"))["status"], "draft")
            with self.assertRaises(SystemExit):
                run("access", "--profile", "intern-search", "--confirm")
            self.store.path.unlink()
            with self.assertRaises(SystemExit):
                run("ask", "Jira")

    def test_mixed_freshness_preserves_warning_and_source(self):
        passages = self.search("403") + self.search("dev-окружение")
        fresh, old = passages
        result = self.generator.generate("Вопрос", passages, audience=INTERN, offline=True)
        self.assertIn(old.review_by, result.answer)
        self.assertEqual(len(result.sources), 2)
        self.assertNotIn("dev-old", result.answer)
        with self.assertRaisesRegex(ValueError, "Conflicting metadata"):
            self.generator.generate("Вопрос", [fresh, replace(fresh, text="other")], audience=INTERN)
        with self.assertRaises(ValueError):
            request_access(self.store, "intern-platform", confirm="false")

    def test_http_client_contract_and_failures(self):
        config = GenerationConfig("secret", base_url="http://localhost:11434/v1/")
        client = ChatCompletionsClient(config)
        with patch("rag.generation.build_opener") as opener:
            response = opener.return_value.open.return_value.__enter__.return_value
            response.read.return_value = b'{"choices":[]}'
            self.assertEqual(client.complete([]), {"choices": []})
            req = opener.return_value.open.call_args.args[0]
            self.assertEqual(req.full_url, "http://localhost:11434/v1/chat/completions")
            self.assertEqual(req.get_header("Authorization"), "Bearer secret")
            for body in (b'x' * 1048577, b'\xff', b'invalid'):
                response.read.return_value = body
                with self.assertRaises(GenerationError):
                    client.complete([])
            for err in (TimeoutError(), HTTPError("url", 429, "limit", {}, None)):
                opener.return_value.open.side_effect = err
                with self.assertRaises(GenerationError):
                    client.complete([])
        self.assertNotIn("secret", repr(config))
        for change in ({"model": ""}, {"api_key": ""}, {"base_url": "http://example.com"},
                       {"timeout_seconds": 0}, {"max_context_chars": 0}):
            with self.assertRaises(ValueError):
                replace(config, **change)
        with patch.dict(os.environ, {"RAG_LLM_API_KEY": "test", "RAG_LLM_MODEL": "test-provider/model"}):
            self.assertEqual(GenerationConfig.from_env().model, "test-provider/model")
