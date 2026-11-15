"""Internal REST adapter. Go supplies actor context after authenticating the user."""

from typing import Literal

from fastapi import Depends, FastAPI, Header
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from pydantic import BaseModel, ConfigDict, Field

from rag.application import Actor, ApplicationError, RagApplication
from .auth import authorized, service_token


class StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)


class ActorInput(StrictModel):
    user_id: str = Field(min_length=1, max_length=128)
    role: Literal["intern", "mentor", "lead"]
    team: str = Field(min_length=1, max_length=128)


class AnswerInput(StrictModel):
    question: str = Field(min_length=1, max_length=4000)
    actor: ActorInput
    request_id: str = Field(default="", pattern=r"^[A-Za-z0-9_.:-]{0,128}$")
    mode: Literal["offline", "llm"] = "offline"
    top_k: int = Field(default=3, ge=1, le=50)


class SearchInput(StrictModel):
    query: str = Field(min_length=1, max_length=4000)
    actor: ActorInput
    request_id: str = Field(default="", pattern=r"^[A-Za-z0-9_.:-]{0,128}$")
    top_k: int = Field(default=3, ge=1, le=50)


class ErrorOutput(BaseModel):
    error: dict[str, str]


class SourceOutput(BaseModel):
    document_id: str
    version_id: str
    chunk_id: str
    title: str
    text: str
    source_url: str | None
    page_number: int | None
    origin: str
    stale: bool
    review_by: str | None
    start_char: int
    end_char: int


class StepOutput(BaseModel):
    text: str
    source_ids: list[str]


class AnswerOutput(BaseModel):
    request_id: str
    status: Literal["answered", "sources_found", "insufficient_evidence", "conflict", "needs_review"]
    mode: Literal["llm", "offline", "rules"]
    steps: list[StepOutput]
    sources: dict[str, SourceOutput]
    next_step: str
    model: str | None
    action: str | None


class SearchOutput(BaseModel):
    request_id: str
    sources: list[SourceOutput]


HTTP_CODES = {"invalid_input": 400, "unauthorized": 401, "resource_exhausted": 429,
              "deadline_exceeded": 504, "temporarily_unavailable": 503}


def error_response(code):
    return JSONResponse(status_code=HTTP_CODES.get(code, 500), content={"error": {"code": code}})


class BodyLimit:
    """Bound chunked bodies too, before FastAPI's JSON parser allocates them."""
    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http" or scope["method"] != "POST":
            return await self.app(scope, receive, send)
        body = bytearray()
        while True:
            message = await receive()
            if message["type"] == "http.disconnect":
                return
            body.extend(message.get("body", b""))
            if len(body) > 65536:
                return await error_response("invalid_input")(scope, receive, send)
            if not message.get("more_body", False):
                break
        delivered = False

        async def replay():
            nonlocal delivered
            if not delivered:
                delivered = True
                return {"type": "http.request", "body": bytes(body), "more_body": False}
            return await receive()

        await self.app(scope, replay, send)


def create_app(application=None, token=None, *, lifespan=None):
    service = application if application is not None else RagApplication()
    token = service_token(token)
    app = FastAPI(title="AI-bud internal RAG API", version="1.0.0", lifespan=lifespan,
                  responses={"default": {"model": ErrorOutput,
                             "description": "Structured error: 400 invalid input, 401 unauthorized, 429 capacity, 503 unavailable, 504 deadline, or 500 internal error."}})
    app.add_middleware(BodyLimit)

    def authenticate(authorization: str = Header(default="")):
        if not authorized(authorization, token):
            raise ApplicationError("unauthorized")

    @app.exception_handler(ApplicationError)
    async def application_error(request, exc):
        return error_response(exc.code)

    @app.exception_handler(RequestValidationError)
    async def validation_error(request, exc):
        return error_response("invalid_input")

    @app.exception_handler(Exception)
    async def internal_error(request, exc):
        return error_response("internal_error")

    @app.middleware("http")
    async def no_cache(request, call_next):
        response = await call_next(request)
        response.headers["Cache-Control"] = "no-store"
        return response

    @app.get("/healthz")
    def health():
        return {"status": "ok"}

    @app.get("/readyz")
    def ready():
        if not service.ready():
            return error_response("temporarily_unavailable")
        return {"status": "ready"}

    @app.post("/v1/answers", dependencies=[Depends(authenticate)], response_model=AnswerOutput)
    def answer(body: AnswerInput):
        return service.answer(body.question, Actor(**body.actor.model_dump()),
                              request_id=body.request_id, mode=body.mode, top_k=body.top_k)

    @app.post("/v1/search", dependencies=[Depends(authenticate)], response_model=SearchOutput)
    def search(body: SearchInput):
        return service.search(body.query, Actor(**body.actor.model_dump()),
                              request_id=body.request_id, top_k=body.top_k)

    return app
