"""Bounded synchronous gRPC transport for the existing synchronous pipeline."""

from concurrent.futures import ThreadPoolExecutor
import logging

import grpc
from grpc_health.v1 import health_pb2, health_pb2_grpc

from rag.application import Actor, ApplicationError
from rag.v1 import rag_pb2 as pb, rag_pb2_grpc
from .auth import authorized, service_token
from .mapping import answer_message, source_message


CODES = {"search_unavailable": grpc.StatusCode.UNAVAILABLE, "model_unconfigured": grpc.StatusCode.FAILED_PRECONDITION, "model_unavailable": grpc.StatusCode.UNAVAILABLE, "model_timeout": grpc.StatusCode.DEADLINE_EXCEEDED, "model_rate_limited": grpc.StatusCode.RESOURCE_EXHAUSTED, "model_invalid_response": grpc.StatusCode.DATA_LOSS, "model_incomplete": grpc.StatusCode.DATA_LOSS,
"invalid_input": grpc.StatusCode.INVALID_ARGUMENT,
         "unauthorized": grpc.StatusCode.UNAUTHENTICATED,
         "resource_exhausted": grpc.StatusCode.RESOURCE_EXHAUSTED,
         "deadline_exceeded": grpc.StatusCode.DEADLINE_EXCEEDED,
         "temporarily_unavailable": grpc.StatusCode.UNAVAILABLE}


class RagServicer(rag_pb2_grpc.RagServiceServicer):
    def __init__(self, application, token):
        self.application = application
        self.token = service_token(token)

    def _call(self, request, context, answer):
        credentials = [v for k, v in context.invocation_metadata() if k == "authorization"]
        if len(credentials) != 1 or not authorized(credentials[0], self.token):
            context.abort(grpc.StatusCode.UNAUTHENTICATED, "unauthorized")
        if not context.is_active():
            context.abort(grpc.StatusCode.CANCELLED, "cancelled")
        actor = Actor(request.actor.user_id, request.actor.role, request.actor.team)
        kwargs = dict(request_id=request.request_id,
                      top_k=request.top_k if request.HasField("top_k") else 3,
                      timeout_seconds=context.time_remaining())
        try:
            if answer:
                modes = {pb.ANSWER_MODE_UNSPECIFIED: "offline", pb.ANSWER_MODE_OFFLINE: "offline",
                         pb.ANSWER_MODE_LLM: "llm"}
                if request.mode not in modes:
                    raise ApplicationError("invalid_input")
                history = [{"role": item.role, "content": item.content} for item in request.history]
                result = self.application.answer(request.question, actor, mode=modes[request.mode], history=history, **kwargs)
                response = answer_message(result)
            else:
                result = self.application.search(request.query, actor, **kwargs)
                response = pb.SearchResponse(request_id=result["request_id"],
                                             sources=[source_message(s) for s in result["sources"]])
        except ApplicationError as exc:
            context.abort(CODES[exc.code], exc.code)
        except Exception:
            logging.getLogger(__name__).exception("RAG request failed")
            context.abort(grpc.StatusCode.INTERNAL, "internal_error")
        if not context.is_active():
            context.abort(grpc.StatusCode.CANCELLED, "cancelled")
        return response

    def Answer(self, request, context):
        return self._call(request, context, True)

    def Search(self, request, context):
        return self._call(request, context, False)


class Health(health_pb2_grpc.HealthServicer):
    def __init__(self, application):
        self.application = application

    def Check(self, request, context):
        if request.service not in ("", "aibud.rag.v1.RagService"):
            context.abort(grpc.StatusCode.NOT_FOUND, "unknown_service")
        status = health_pb2.HealthCheckResponse.SERVING if self.application.ready() else health_pb2.HealthCheckResponse.NOT_SERVING
        return health_pb2.HealthCheckResponse(status=status)


def create_server(application, token, address="127.0.0.1:50051", *, workers=16):
    # Plaintext transport is for loopback / the private local Compose network.
    # Use TLS or a service-mesh TLS boundary before deploying across hosts.
    executor = ThreadPoolExecutor(max_workers=workers)
    server = grpc.server(executor, maximum_concurrent_rpcs=workers,
                         options=[("grpc.max_receive_message_length", 65536),
                                  ("grpc.max_send_message_length", 4 * 1024 * 1024)])
    rag_pb2_grpc.add_RagServiceServicer_to_server(RagServicer(application, token), server)
    health_pb2_grpc.add_HealthServicer_to_server(Health(application), server)
    port = server.add_insecure_port(address)
    if not port:
        executor.shutdown(wait=False)
        raise RuntimeError("Could not bind RAG gRPC address")
    return server, executor, port
