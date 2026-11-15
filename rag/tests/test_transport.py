from pathlib import Path
import tempfile
import unittest

try:
    import grpc
    from fastapi.testclient import TestClient
    from grpc_health.v1 import health_pb2, health_pb2_grpc
    from rag.v1 import rag_pb2 as pb, rag_pb2_grpc
    from rag.transport.grpc_server import create_server
    from rag.transport.rest import create_app
except ImportError as exc:
    raise unittest.SkipTest("Install rag[server,dev] to run transport tests") from exc

from rag.application import RagApplication
from rag.ingestion.indexing import DEMO_MANIFEST, index_manifest
from rag.storage import SQLiteStore


class TransportTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.store = SQLiteStore(Path(temp.name) / "index.db")
        index_manifest(DEMO_MANIFEST, self.store)
        self.application = RagApplication(self.store)
        self.rest = TestClient(create_app(self.application, "test-token"))
        self.addCleanup(self.rest.close)
        server, executor, port = create_server(self.application, "test-token", "127.0.0.1:0")
        server.start()
        self.addCleanup(executor.shutdown, wait=True)
        self.addCleanup(lambda: server.stop(0).wait())
        self.channel = grpc.insecure_channel(f"127.0.0.1:{port}")
        self.addCleanup(self.channel.close)
        self.grpc = rag_pb2_grpc.RagServiceStub(self.channel)
        self.headers = {"Authorization": "Bearer test-token"}
        self.metadata = (("authorization", "Bearer test-token"),)
        self.actor = dict(user_id="employee-42", role="intern", team="platform")

    def test_rest_and_grpc_share_answer_and_search(self):
        body = dict(question="Jira 403", actor=self.actor, request_id="shared-id", mode="offline", top_k=3)
        rest = self.rest.post("/v1/answers", json=body, headers=self.headers)
        self.assertEqual(rest.status_code, 200, rest.text)
        self.assertEqual(rest.headers["cache-control"], "no-store")
        reply = self.grpc.Answer(pb.AnswerRequest(**dict(body, mode=pb.ANSWER_MODE_OFFLINE)), metadata=self.metadata, timeout=2)
        self.assertEqual(reply.request_id, rest.json()["request_id"])
        self.assertEqual(reply.status, pb.ANSWER_STATUS_SOURCES_FOUND)
        self.assertEqual(reply.sources["S1"].text, rest.json()["sources"]["S1"]["text"])
        self.assertEqual(reply.action, rest.json()["action"])
        self.assertFalse(reply.HasField("model"))
        for query in ("Jira 403", "MENTOR-482"):
            rest = self.rest.post("/v1/search", json=dict(query=query, actor=self.actor), headers=self.headers)
            rpc = self.grpc.Search(pb.SearchRequest(query=query, actor=self.actor), metadata=self.metadata, timeout=2)
            self.assertEqual(len(rpc.sources), len(rest.json()["sources"]))
            if query == "MENTOR-482": self.assertEqual(len(rpc.sources), 0)

    def test_authentication_and_invalid_input(self):
        body = dict(question="Jira 403", actor=self.actor)
        for headers in ({}, {"Authorization": "Bearer wrong"}):
            self.assertEqual(self.rest.post("/v1/answers", json=body, headers=headers).status_code, 401)
        with self.assertRaises(grpc.RpcError) as error:
            self.grpc.Answer(pb.AnswerRequest(**body), timeout=2)
        self.assertEqual(error.exception.code(), grpc.StatusCode.UNAUTHENTICATED)
        for change in ({"top_k": 0}, {"top_k": True}, {"mode": "invalid"}, {"extra": "field"},
                       {"question": "\u0000"}, {"actor": dict(self.actor, role="admin")}):
            response = self.rest.post("/v1/answers", json=dict(body, **change), headers=self.headers)
            self.assertEqual(response.status_code, 400, response.text)
        for request in (pb.AnswerRequest(**body, top_k=0), pb.AnswerRequest(**body, mode=99), pb.AnswerRequest(question="Jira")):
            with self.assertRaises(grpc.RpcError) as error:
                self.grpc.Answer(request, metadata=self.metadata, timeout=2)
            self.assertEqual(error.exception.code(), grpc.StatusCode.INVALID_ARGUMENT)
        response = self.rest.post("/v1/answers", content="x" * 70000, headers=self.headers)
        self.assertEqual(response.status_code, 400)

    def test_health_and_missing_index(self):
        health = health_pb2_grpc.HealthStub(self.channel)
        self.assertEqual(self.rest.get("/healthz").status_code, 200)
        self.assertEqual(self.rest.get("/readyz").status_code, 200)
        self.assertEqual(health.Check(health_pb2.HealthCheckRequest(), timeout=2).status, health_pb2.HealthCheckResponse.SERVING)
        self.store.path.unlink()
        self.assertEqual(self.rest.get("/readyz").status_code, 503)
        self.assertEqual(health.Check(health_pb2.HealthCheckRequest(), timeout=2).status, health_pb2.HealthCheckResponse.NOT_SERVING)
        response = self.rest.post("/v1/answers", json=dict(question="Jira 403", actor=self.actor), headers=self.headers)
        self.assertEqual(response.status_code, 503)
        self.assertNotIn(str(self.store.path), response.text)
        with self.assertRaises(grpc.RpcError) as error:
            self.grpc.Answer(pb.AnswerRequest(question="Jira 403", actor=self.actor), metadata=self.metadata, timeout=2)
        self.assertEqual(error.exception.code(), grpc.StatusCode.UNAVAILABLE)

    def test_grpc_deadline(self):
        import time
        from unittest.mock import patch
        original = self.application.search
        def slow(*args, **kwargs):
            time.sleep(0.1)
            return original(*args, **kwargs)
        with patch.object(self.application, "search", side_effect=slow):
            with self.assertRaises(grpc.RpcError) as error:
                self.grpc.Search(pb.SearchRequest(query="Jira", actor=self.actor), metadata=self.metadata, timeout=0.01)
            self.assertEqual(error.exception.code(), grpc.StatusCode.DEADLINE_EXCEEDED)

    def test_token_required_at_startup(self):
        with self.assertRaises(ValueError): create_app(self.application, "")
