import json
import math
import os
import unittest
from unittest.mock import Mock, patch
from urllib.error import HTTPError, URLError

from rag.embeddings import (EmbeddingClient, EmbeddingConfig, EmbeddingError,
                            MAX_RESPONSE_BYTES, normalize_vector)


class EmbeddingTests(unittest.TestCase):
    def setUp(self):
        self.config = EmbeddingConfig("secret", dimensions=2, batch_size=2)

    @staticmethod
    def response(data):
        response = Mock()
        response.__enter__ = Mock(return_value=response)
        response.__exit__ = Mock(return_value=False)
        response.read.return_value = json.dumps({"data": data}).encode()
        return response

    def test_batching_restores_provider_order_and_normalizes_vectors(self):
        first = self.response([{"index": 1, "embedding": [0, 4]},
                               {"index": 0, "embedding": [3, 0]}])
        second = self.response([{"index": 0, "embedding": [3, 4]}])
        with patch("rag.embeddings.build_opener") as opener:
            opener.return_value.open.side_effect = [first, second]
            result = EmbeddingClient(self.config).embed(["фрагмент 1", "фрагмент 2", "вопрос"])
        self.assertEqual(result, [[1, 0], [0, 1], [0.6, 0.8]])
        requests = opener.return_value.open.call_args_list
        self.assertEqual([json.loads(call.args[0].data)["input"] for call in requests],
                         [["фрагмент 1", "фрагмент 2"], ["вопрос"]])
        payload = json.loads(requests[0].args[0].data)
        self.assertEqual(payload["dimensions"], 2)
        self.assertEqual(payload["encoding_format"], "float")
        self.assertEqual(requests[0].args[0].full_url, self.config.base_url + "/embeddings")
        self.assertEqual(requests[0].args[0].get_header("Authorization"), "Bearer secret")
        self.assertIsNone(opener.call_args.args[0].redirect_request(None, None, 302, "", None, "https://elsewhere"))
        self.assertNotIn("secret", repr(self.config))

    def test_rejects_invalid_vectors_and_provider_indices(self):
        for vector in ([0, 0], [1], [True, 1], [float("nan"), 1], [float("inf"), 1],
                       [10 ** 500, 1], ["x", 1]):
            with self.subTest(vector=vector), self.assertRaises(EmbeddingError):
                normalize_vector(vector, 2)
        bad_responses = [[], [{"index": 1, "embedding": [1, 0]}],
                         [{"index": True, "embedding": [1, 0]}],
                         [{"index": 0, "embedding": [1]}], [{"embedding": [1, 0]}],
                         [{"index": 0, "embedding": [1, 0]}, {"index": 0, "embedding": [1, 0]}]]
        for data in bad_responses:
            with self.subTest(data=data), patch("rag.embeddings.build_opener") as opener:
                opener.return_value.open.return_value = self.response(data)
                with self.assertRaises(EmbeddingError):
                    EmbeddingClient(self.config).embed(["вопрос"])

    def test_network_json_and_response_limit_errors_are_sanitized(self):
        client = EmbeddingClient(self.config)
        for error in (HTTPError("private-url", 401, "secret", {}, None),
                      URLError("private-host"), TimeoutError()):
            with self.subTest(error=type(error)), patch("rag.embeddings.build_opener") as opener:
                opener.return_value.open.side_effect = error
                with self.assertRaises(EmbeddingError) as raised:
                    client.embed(["вопрос"])
                self.assertNotIn("secret", str(raised.exception))
                self.assertNotIn("private", str(raised.exception))
        for body in (b"not json", b"[]", b"\xff", b"x" * (MAX_RESPONSE_BYTES + 1)):
            with self.subTest(body_size=len(body)), patch("rag.embeddings.build_opener") as opener:
                response = self.response([])
                response.read.return_value = body
                opener.return_value.open.return_value = response
                with self.assertRaises(EmbeddingError):
                    client.embed(["вопрос"])

    def test_input_validation_and_deadline(self):
        client = EmbeddingClient(self.config)
        with patch("rag.embeddings.build_opener") as opener:
            self.assertEqual(client.embed([]), [])
            for texts in ([""], [" "], [None]):
                with self.assertRaises(EmbeddingError):
                    client.embed(texts)
            for timeout in (0, -1, float("nan")):
                with self.assertRaises(EmbeddingError):
                    client.embed(["вопрос"], timeout_seconds=timeout)
            opener.assert_not_called()
            opener.return_value.open.return_value = self.response([{"index": 0, "embedding": [1, 0]}])
            client.embed(["вопрос"], timeout_seconds=0.5)
            self.assertEqual(opener.return_value.open.call_args.kwargs["timeout"], 0.5)
        with self.assertRaises(EmbeddingError):
            EmbeddingClient(EmbeddingConfig()).embed(["вопрос"])

    def test_configuration_identity_and_chat_independence(self):
        with patch.dict(os.environ, {"RAG_LLM_API_KEY": "chat-key",
                                     "RAG_LLM_BASE_URL": "https://chat.example/v1"}, clear=True):
            config = EmbeddingConfig.from_env()
            self.assertEqual(config.api_key, "chat-key")
            self.assertEqual(config.base_url, "https://openrouter.ai/api/v1")
            self.assertEqual(config.dimensions, 1536)
            self.assertNotIn("chat-key", config.identity)
            with patch.dict(os.environ, {"RAG_EMBEDDING_API_KEY": "embedding-key"}):
                self.assertEqual(EmbeddingConfig.from_env().api_key, "embedding-key")
                self.assertEqual(EmbeddingConfig.from_env().identity, config.identity)
        for settings in ({"dimensions": 2001}, {"dimensions": True}, {"batch_size": 0},
                         {"timeout_seconds": math.inf}, {"api_key": "bad key"},
                         {"base_url": "http://external.example/v1"},
                         {"base_url": "https://user:secret@example.com/v1"}):
            with self.subTest(settings=settings), self.assertRaises(ValueError):
                EmbeddingConfig(**settings)


if __name__ == "__main__":
    unittest.main()
