# AI-buddy frontend

React 19.2 + TypeScript 5.9. Static production bundle built with Vite; the existing Caddy gateway is the only frontend HTTP server. Dependencies are pinned in `package-lock.json`. No dev server, CDN, browser-side LLM key, or Python service credential is required.

## Run the complete application

From the repository root, with Docker Engine and Compose 2.20+ (supports `include`):

```sh
docker compose -f docker-compose.dev.yaml up --build -d
```

Open **http://localhost/**. The one-shot `frontend-build` container runs `npm ci && npm run build`; Caddy serves `frontend/dist`. The root Compose defines API, RAG, indexer, databases and the integration mock. Gateway waits for the frontend build and a healthy Go API. Do not launch a second copy of `backend/compose.yaml` on the same ports. The mock grants eligible requests after approximately 30 seconds; click **Проверить статус** to read its result.

```sh
docker compose -f docker-compose.dev.yaml logs frontend-build rpp-buddy-gateway api rag
docker compose -f docker-compose.dev.yaml down
```

To rebuild only frontend files after editing:

```sh
docker compose -f docker-compose.dev.yaml run --rm frontend-build
```

For a local build without Docker, use Node 22.12+:

```sh
cd frontend
npm ci
npm run build
npm run coverage
```

The gateway mounts the existing `frontend` folder and serves its `dist` directory. Its `/api/*`, `/health`, `/ready` routes proxy to Go (`API_UPSTREAM`, default `api:8080`). For a native Go API, run the same gateway with `API_UPSTREAM=host.docker.internal:8080`. The browser always calls the same origin. A successful gateway page does not imply that the API or model is ready; connection errors are visible in the interface.

## Demo profiles

Open **Демонстрация прав доступа** and choose **Демо-профиль**. The regular flow starts as the Platform intern. These are synthetic identities from the existing backend directory, not a new employee/auth system.

| Profile                     | Backend token          | Request allowed      |
| --------------------------- | ---------------------- | -------------------- |
| Стажёр · Platform (default) | `demo-intern-platform` | Read `jira:BUD`      |
| Стажёр · Search             | `demo-intern-search`   | None                 |
| Наставник · Platform        | `demo-mentor-platform` | None                 |

The selected demo token is stored in `sessionStorage`. Dialog turns, sources and errors are persisted by Go in PostgreSQL and restored when the assistant mounts. Profile changes clear visible user-specific state. Role and team are resolved by `/api/me` and never sent as user-controlled fields to the RAG endpoint. The backend remains authoritative for every operation.

## Verify the scenario

1. With the Platform intern, click **Не открывается Jira**. The default is an LLM answer. Read the cited answer and its **Учебный материал** label.
2. Click **Подготовить заявку на чтение BUD**. Review system/project, permission and reason, then **Проверить заявку**. This creates the backend's saved proposal; it does not submit to the access service.
3. On the separate confirmation step, check the confirmation checkbox and click **Подтвердить и отправить**. The card shows the returned ticket ID and **На согласовании**. This means submitted, not access granted.
4. Open **Мои заявки**, reload the page, reopen the saved request. **Обновить данные** reads persisted state; **Проверить статус** calls reconciliation for that same request. After the mock grants access, the next step becomes returning to the first task.
5. In **Следующий шаг**, inspect the three process stages and server-provided next steps. This is the selected access scenario, not a complete onboarding management system.
6. Create another proposal and cancel it before confirmation. The saved cancellation is visible. Version conflicts ask the user to reload the request instead of silently confirming new data.
7. In the assistant, ask **Кого позвать на ревью?**: inspect both conflicting sources. Ask **dev-окружение**: inspect expiry and escalation. Ask an unknown question: the assistant directs to mentor → lead → HRBP.
8. In **База знаний**, search allowed source excerpts. Switch between intern/mentor for `MENTOR-482` and Platform/Search for `SEARCH-730` to check ACL isolation. The UI never explains that an unavailable document exists.
9. For online answers, set `RAG_LLM_API_KEY` before starting Compose in local `rag/.env`. Without provider configuration, the error remains visible; **Найти инструкцию без модели** requires an explicit click. LLM calls are never automatically retried.
10. During a slow answer, navigation and input remain usable, duplicate submission is disabled, and **Остановить ожидание** aborts the browser wait. Server work may already have started; retrying an uncertain submission uses the same idempotency key; explicitly repeating a saved failed query starts a new attempt.

## REST compatibility

`src/api.ts` is the explicit API client. It supports all public Go operations:

- `GET /api/me`, `/api/onboarding`, `/health`, `/ready`;
- `POST /api/rag/answers`, `/api/rag/search`;
- `GET /api/access-requests`, `/api/access-requests/{id}`;
- `POST /api/access-requests` with `Idempotency-Key`;
- `POST .../{id}/confirm` and `.../{id}/cancel` with the saved version;
- `POST .../{id}/refresh` without a body.

The Python `/v1/answers` and `/v1/search` endpoints are internal. Browser AI operations use their existing public Go equivalents, which delegate to Python over gRPC. Exposing the internal service token or accepting a browser-provided role would bypass that trust boundary.

All request states, ticket statuses, RAG business outcomes and documented error codes are represented. HTTP 202 is a saved uncertain/in-progress result. Proposal retries retain the same payload/key while the component is mounted; after a reload, inspect the server list before preparing a new proposal. The frontend does not automatically submit, refresh, or recreate a request. Source bodies and model text are rendered as text, with only safe http(s) original links. No synthetic UI success replaces an API result.

## Tests, coverage and requirements

```sh
cd frontend
npm run coverage
```

Vitest + Testing Library exercise every API operation, the complete confirmation flow, retries, UTF-8 limits, version conflicts, auth changes, errors, cancellation, search, citations, rendering safety and unmount isolation. V8 reports all handwritten TypeScript/TSX files in `src` (including bootstrap); dependencies, tests and generated output are excluded. CI requires **>80%** lines/statements/functions (81% gate) and 75% branches. Report: `frontend/coverage/lcov.info`, summary: `frontend/coverage/coverage-summary.json`. A checked-in snapshot is supplied in `docs/frontend-coverage.info` and `docs/frontend-coverage-summary.json`.

To refresh the reviewable snapshots from the repository root:

```sh
(cd frontend && npm run report)
```

The case's project-wide coverage must be calculated from covered/executable line counts across frontend, Go and Python, rather than averaging their percentages. This frontend report alone does not establish total project coverage.

The automated component tests use contract responses. They validate frontend behavior, not live service availability. Follow the Compose walkthrough above for a real full-stack run. Requirements and behavior mapping: [frontend SDD](../docs/frontend-sdd.md). Public contracts: [backend OpenAPI](../backend/contracts/backend.openapi.yaml), [RAG API](../docs/rag-api.md).

The source-count and mode selectors are hidden. The client uses a fixed search limit and requests `llm`; the public API retains explicit offline/search operations. On a transient gateway error the shell retries connecting for up to 40 seconds, then offers a manual reconnect. General replies have `status=conversation` and need no citations. See [current SDD](../docs/frontend-sdd.md) and [whole-application coverage](../docs/COVERAGE.md).
