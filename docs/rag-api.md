# RAG REST and gRPC API

The frontend calls Go over REST. Go authenticates the employee and calls Python's
`aibud.rag.v1.RagService` over gRPC. Python also exposes an internal REST API for
tools. Both adapters use `rag.application.RagApplication` and the existing
retrieval/generation pipeline. Answers and searches do not create access requests.

## Run the complete local stack

From the repository root:

```sh
docker compose -f backend/compose.yaml up --build -d
curl http://localhost:8001/ready
curl http://localhost:8080/ready
curl -sS http://localhost:8080/api/rag/answers \
  -H 'Authorization: Bearer demo-intern-platform' \
  -H 'Content-Type: application/json' \
  -d '{"question":"Не открывается Jira, ошибка 403","mode":"offline"}'
```

Compose publishes a demo knowledge snapshot through `rag-index`, then starts
Python with the index mounted read-only. Ports 8080 (Go), 8001 (Python REST), and
50051 (Python gRPC) are published only on loopback. Offline is the default mode.
To request online generation, configure `RAG_LLM_API_KEY` before starting Compose
and send `"mode":"llm"`. Missing provider configuration returns 503 when generation
is needed; it never silently falls back to offline output.

The synthetic demo accounts are:

| Bearer token | Role | Team | Access proposal permitted |
|---|---|---|---|
| `demo-intern-platform` | intern | platform | `jira:BUD`, `read` |
| `demo-intern-search` | intern | search | none |
| `demo-mentor-platform` | mentor | platform | none |
| `demo-alex` | intern | notifications | notifications, read |
| `demo-sam` | intern | payments | payments, read |

Go rejects `actor`, `role`, `team`, and `user_id` fields in public RAG requests.
It obtains them from its authenticated directory. Python accepts delegated actor
context only with `Authorization: Bearer <RAG_SERVICE_TOKEN>` (the same metadata
key for gRPC). The local Compose credential is `local-rag-token`; override it with
`RAG_SERVICE_TOKEN`. The current binary and plaintext gRPC transport are for local
demo use. Cross-host deployments need TLS/service identity and real employee auth.

## Python-only development

From the repository root, using Python 3.11+:

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -e './rag[server,dev]'
set -a
. ./rag/.env
set +a
docker compose -f docker-compose.dev.yaml --profile rag up -d rag-db
python -m rag index
export RAG_SERVICE_TOKEN=local-rag-token
python -m rag.transport.server
```

This starts REST at `127.0.0.1:8001` and gRPC at `127.0.0.1:50051`. Configure
`RAG_DATABASE_URL`, `RAG_HTTP_HOST`, `RAG_HTTP_PORT`, and `RAG_GRPC_ADDR` as needed.
Create `rag/.env` from `rag/.env.example` and set an embedding API key before
running these commands; see [embedding configuration](../rag/README.md#семантический-поиск).
Offline answers still require embeddings and PostgreSQL; they skip chat generation.
To start REST alone: `uvicorn rag.transport.rest:create_app --factory --port 8001`.
The REST OpenAPI schema is available at `http://localhost:8001/openapi.json`.

```sh
curl -sS http://localhost:8001/v1/answers \
  -H 'Authorization: Bearer local-rag-token' \
  -H 'Content-Type: application/json' \
  -d '{"question":"Jira 403","mode":"offline","top_k":3,"actor":{"user_id":"intern-platform","role":"intern","team":"platform"}}'
```

Set `RAG_GRPC_TARGET=127.0.0.1:50051` and the matching `RAG_SERVICE_TOKEN` when
running Go locally. If `RAG_GRPC_TARGET` is omitted, Go's RAG endpoints return 503;
the existing access-request API continues to run.

## Endpoints and results

| Public Go REST | Internal Python REST | gRPC |
|---|---|---|
| POST `/api/rag/answers` | POST `/v1/answers` | `Answer` |
| POST `/api/rag/search` | POST `/v1/search` | `Search` |

Answer input: `question` (1–4000 characters, nonblank, no NUL), optional `mode`
(`offline` by default, or `llm`), optional `top_k` (1–50, default 3). Search uses
`query` and optional `top_k`. Python additionally requires `actor` and accepts an
optional correlation `request_id` (1–128 ASCII letters, digits, `_`, `.`, `:`, `-`).
An omitted/empty ID is generated. IDs are correlation IDs, not idempotency keys:
retrying an LLM request can incur another provider call.

Answers retain `status`, `mode`, `steps`, `sources`, `next_step`, `model`, `action`
and add `request_id`. Each step references keys in `sources`. Source projections
contain document/version/chunk IDs, title, excerpt, URL/page, character offsets,
origin and freshness; internal filesystem paths, roles and rule metadata are omitted.
Search returns `request_id` and an ordered `sources` array. Conflict evidence may
expand the result beyond `top_k`, matching the existing retriever.

`answered`, `sources_found`, `insufficient_evidence`, `conflict`, and `needs_review`
are successful business outcomes (HTTP 200 / gRPC OK). `mode` reports actual
execution: `llm`, `offline`, or `rules`.

| Error code | Python REST | gRPC | Go REST |
|---|---|---|---|
| invalid_input | 400 | INVALID_ARGUMENT | 400 |
| unauthorized | 401 | UNAUTHENTICATED | 503 for a bad Go service credential |
| resource_exhausted | 429 | RESOURCE_EXHAUSTED | 429 |
| temporarily_unavailable | 503 | UNAVAILABLE | 503 |
| deadline_exceeded | 504 | DEADLINE_EXCEEDED | 504 |
| internal_error | 500 | INTERNAL | 500 |

Errors use `{"error":{"code":"..."}}`. Go returns 401 for invalid employee auth.
`/health` checks the Python process, `/ready` checks the readable populated index.
The standard gRPC health `Check` supports the empty service and
`aibud.rag.v1.RagService`; `Watch` is not implemented. Readiness does not make a paid
LLM call. Go's existing readiness endpoint continues to check its database.

The shared application admits at most 16 concurrent operations. Request bodies
are limited to 64 KiB. The Go RPC budget is 30 seconds, its RAG HTTP handler allows
35 seconds, and its server write timeout is 45 seconds. Python passes the remaining
budget to generation with a maximum provider socket timeout of 25 seconds.
There are no automatic LLM retries. The synchronous provider client cannot forcibly
interrupt a running thread or guarantee a wall-clock bound for a slowly trickling
response; cancellation ends the RPC but in-flight work may finish afterward. Use
a cancellable async provider client before adding streaming or stricter deadlines.

## Confirming a suggested action

`preview_jira_access` is a hint to show a proposal form. Go's synthetic catalog maps
it to `resource: "jira:BUD"`, `access: "read"`. The user reviews the reason, then:

```sh
curl -sS http://localhost:8080/api/access-requests \
  -H 'Authorization: Bearer demo-intern-platform' \
  -H 'Idempotency-Key: jira-first-task' -H 'Content-Type: application/json' \
  -d '{"resource":"jira:BUD","access":"read","reason":"First task"}'
# Use the returned id and version in POST /api/access-requests/{id}/confirm.
```

The existing Go policy, confirmation, idempotency and reconciliation govern this
operation. Python's access adapter remains a CLI demo and is not called by
either network transport. Source freshness is advisory for this demo suggestion;
Go's action authorization is its own policy, not a live check of the Python index.

## Contracts and verification

The shared source is `contracts/proto/rag/v1/rag.proto`. Generated Go and Python
bindings are committed. With the server/dev extras and Go >= 1.27 installed:

```sh
python scripts/generate_rag_proto.py
python -m unittest discover -s rag/tests -v
export RAG_TEST_PYTHON="$PWD/.venv/bin/python"
make -C backend rag-integration
make -C backend test
make -C backend vet
```

The generation script uses pinned Go plugins; set `GO` to a Go binary if needed.
The `rag_integration` test launches Python, builds a temporary demo index, and
checks Go REST → gRPC → Python, ACL filtering, and the separate confirmation flow.
It needs no PostgreSQL or provider key. The existing `integration` tests still
require `TEST_DATABASE_URL`.
