"""Run both internal transports with one shared application and concurrency limit."""

import asyncio
from contextlib import asynccontextmanager
import logging
import os

import uvicorn

from rag.application import RagApplication
from .auth import service_token
from .grpc_server import create_server
from .rest import create_app


def main():
    logging.basicConfig(level=logging.INFO)
    token = service_token()
    application = RagApplication()
    server, executor, _ = create_server(application, token, os.environ.get("RAG_GRPC_ADDR", "127.0.0.1:50051"))
    def stop():
        server.stop(grace=30).wait()
        executor.shutdown(wait=True, cancel_futures=True)

    @asynccontextmanager
    async def lifespan(app):
        server.start()
        try:
            yield
        finally:
            # Uvicorn re-raises SIGTERM after lifespan shutdown. Cleanup must
            # finish here, before control returns from uvicorn.run().
            await asyncio.to_thread(stop)
            logging.getLogger(__name__).info("RAG gRPC stopped")

    try:
        uvicorn.run(create_app(application, token, lifespan=lifespan), host=os.environ.get("RAG_HTTP_HOST", "127.0.0.1"),
                    port=int(os.environ.get("RAG_HTTP_PORT", "8001")), limit_concurrency=64,
                    timeout_graceful_shutdown=35)
    finally:
        stop()


if __name__ == "__main__":
    main()
