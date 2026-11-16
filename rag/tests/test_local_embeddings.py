from concurrent.futures import ThreadPoolExecutor
import json
import os
import sys
from types import ModuleType
import unittest
from unittest.mock import Mock, patch

from rag.embeddings import EmbeddingError, create_embedder
from rag.local_embeddings import LocalEmbeddingClient, LocalEmbeddingConfig, _load_model
from rag.vector_storage import PgVectorStore


class LocalEmbeddingTests(unittest.TestCase):
    def setUp(self):
        _load_model.cache_clear()
        self.addCleanup(_load_model.cache_clear)
        self.config = LocalEmbeddingConfig(model="fixture-model", dimensions=3, batch_size=2)
        self.model = Mock()
        self.model.get_sentence_embedding_dimension.return_value = 3
        self.module = ModuleType("sentence_transformers")
        self.module.SentenceTransformer = Mock(return_value=self.model)

    def test_default_provider_is_bge_m3_without_api_credentials(self):
        with patch.dict(os.environ, {}, clear=True):
            client = create_embedder()
            self.assertIsInstance(client, LocalEmbeddingClient)
            self.assertEqual(client.config.model, "BAAI/bge-m3")
            self.assertEqual(client.config.dimensions, 1024)

    def test_batches_raw_text_and_normalizes_dense_vectors_without_network_api(self):
        self.model.encode.side_effect = [Mock(tolist=lambda: [[3, 0, 0], [0, 4, 0]]),
                                         Mock(tolist=lambda: [[0, 0, 2]])]
        texts = ["Меня не пускает в трекер", "Заголовок\n\nИнструкция", "Ещё вопрос"]
        with patch.dict(sys.modules, {"sentence_transformers": self.module}):
            vectors = LocalEmbeddingClient(self.config).embed(texts)
        self.assertEqual(vectors, [[1, 0, 0], [0, 1, 0], [0, 0, 1]])
        self.assertEqual([call.args[0] for call in self.model.encode.call_args_list], [texts[:2], texts[2:]])
        for call in self.model.encode.call_args_list:
            self.assertTrue(call.kwargs["normalize_embeddings"])
            self.assertFalse(call.kwargs["show_progress_bar"])
        self.assertFalse(self.module.SentenceTransformer.call_args.kwargs["trust_remote_code"])

    def test_cached_embeddings_are_bounded_and_return_independent_vectors(self):
        self.model.encode.return_value = Mock(tolist=lambda: [[1, 0, 0]])
        with patch.dict(sys.modules, {"sentence_transformers": self.module}):
            client = LocalEmbeddingClient(self.config)
            first = client.embed(["Jira 403"])
            first[0][0] = 999
            self.assertEqual(client.embed(["Jira 403"]), [[1, 0, 0]])
            self.model.encode.assert_called_once()
            for i in range(35):
                client.embed([f"question {i}"])
            self.assertEqual(len(client._cache), 32)
            self.assertNotIn(("Jira 403",), client._cache)

    def test_model_is_loaded_once_across_clients_and_concurrent_warmups(self):
        with patch.dict(sys.modules, {"sentence_transformers": self.module}):
            with ThreadPoolExecutor(max_workers=4) as workers:
                list(workers.map(lambda _: LocalEmbeddingClient(self.config).warmup(), range(8)))
        self.module.SentenceTransformer.assert_called_once()

    def test_invalid_input_and_deadline_do_not_load_model(self):
        client = LocalEmbeddingClient(self.config)
        with patch("rag.local_embeddings._load_model") as load:
            self.assertEqual(client.embed([]), [])
            for texts in ([""], [" "], [None]):
                with self.assertRaises(EmbeddingError):
                    client.embed(texts)
            for timeout in (0, -1, float("nan")):
                with self.assertRaises(EmbeddingError):
                    client.embed(["вопрос"], timeout_seconds=timeout)
            load.assert_not_called()

    def test_deadline_expiring_during_inference_is_reported(self):
        self.model.encode.return_value = Mock(tolist=lambda: [[1, 0, 0]])
        with (patch.dict(sys.modules, {"sentence_transformers": self.module}),
              patch("rag.local_embeddings.time.monotonic", side_effect=[0, 0, 0, 2])):
            with self.assertRaisesRegex(EmbeddingError, "deadline"):
                LocalEmbeddingClient(self.config).embed(["вопрос"], timeout_seconds=1)

    def test_wrong_output_dimensions_and_inference_failures_are_errors(self):
        with patch.dict(sys.modules, {"sentence_transformers": self.module}):
            self.model.get_sentence_embedding_dimension.return_value = 2
            with self.assertRaisesRegex(EmbeddingError, "DIMENSIONS"):
                LocalEmbeddingClient(self.config).warmup()
            self.model.get_sentence_embedding_dimension.return_value = 3
            self.model.encode.side_effect = RuntimeError("private runtime details")
            with self.assertRaises(EmbeddingError) as error:
                LocalEmbeddingClient(self.config).embed(["вопрос"])
            self.assertNotIn("private", str(error.exception))
        with self.assertRaises(ValueError):
            LocalEmbeddingConfig(dimensions=1536)

    def test_missing_local_dependency_is_actionable(self):
        with patch.dict(sys.modules, {"sentence_transformers": None}):
            with self.assertRaisesRegex(EmbeddingError, "local.*dependencies"):
                LocalEmbeddingClient(self.config).warmup()

    def test_readiness_with_a_local_model_does_not_require_an_api_key(self):
        store = PgVectorStore(embedder=LocalEmbeddingClient())
        db = Mock()
        db.execute.side_effect = [Mock(fetchall=lambda: [{"embedding_space": store.embedding_identity}]),
                                  Mock(fetchone=lambda: {"exists": 1})]
        with patch.object(store, "connect") as connect:
            connect.return_value.__enter__.return_value = db
            self.assertTrue(store.ready())

    def test_identity_excludes_device_cache_and_chat_credentials(self):
        config = LocalEmbeddingConfig()
        self.assertEqual(json.loads(config.identity), ["local-dense", "BAAI/bge-m3", config.revision, 1024])
        self.assertEqual(config.identity, LocalEmbeddingConfig(device="cuda", cache_dir="/models").identity)


if __name__ == "__main__":
    unittest.main()
