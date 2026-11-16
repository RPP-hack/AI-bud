# AI-bud · RAG для онбординга стажёра

REST и gRPC сервер, подключение к Go и примеры запросов: [RAG API](../docs/rag-api.md).

Помощник отвечает на вопросы о первых неделях стажировки по материалам, доступным сотруднику. Основной сценарий — стажёр учебной команды Platform не может открыть Jira-проект для первой задачи. Помощник находит инструкцию, показывает черновик заявки и после явного подтверждения сохраняет результат в имитаторе сервиса доступов.

## Запуск за минуту

Команды ниже выполняются из корня репозитория. Нужны Python 3.11+, PostgreSQL 17 с pgvector 0.8+ и ключ OpenRouter для embeddings. Скопируйте `rag/.env.example` в `rag/.env` и заполните `RAG_EMBEDDING_API_KEY` (или `RAG_LLM_API_KEY`). Переменные окружения для CLI загружаются явно.

```sh
python3 -m venv .venv
. .venv/bin/activate
python3 -m pip install -e ./rag
set -a
. ./rag/.env
set +a
docker compose -f docker-compose.dev.yaml --profile rag up -d rag-db
python3 -m rag index
python3 -m rag ask 'Не открывается Jira, ошибка 403' --offline
python3 -m rag access
python3 -m rag access --confirm
python3 -m rag access
```

Первый ответ ссылается на инструкцию по BUD. Команда `access` показывает систему, проект, уровень прав и обоснование. После подтверждения появляется номер вида `BUD-ACCESS-1` и статус `pending` («На согласовании»). Следующий шаг — дождаться решения владельца проекта. Номер сохраняется между запусками, включая повторное подтверждение. Выдача прав лежит за границей этого сценария.

Команды используют тестовый профиль `intern-platform`. Флаг `--profile` также принимает `mentor-platform` и `intern-search`. Пароли для локального демонстратора не нужны. При подключении к веб-сервису идентификатор сотрудника, роль и команду передаёт авторизованный бэкенд. Выбор профиля из CLI предназначен для проверки на синтетических данных.

Размеченные вопросы, автоматические метрики и проверка реальной модели описаны в [evals](evals/README.md). Границы защиты и ограничения детекторов — в [guardrails](../docs/rag-guardrails.md).

Офлайн-прогон качества использует прежний SQLite-адаптер как изолированный тестовый стенд и не требует API-ключа. Он проверяет старые сценарии и guardrails, но не качество нового семантического поиска:

```sh
python3 -m rag.evaluation run --output rag/evals/reports/offline.json
```

## Docker

```sh
docker compose -f docker-compose.dev.yaml --env-file rag/.env --profile rag run --build --rm rag-cli index
docker compose -f docker-compose.dev.yaml --env-file rag/.env --profile rag run --rm rag-cli ask 'Не открывается Jira, ошибка 403' --offline
docker compose -f docker-compose.dev.yaml --env-file rag/.env --profile rag run --rm rag-cli access
docker compose -f docker-compose.dev.yaml --env-file rag/.env --profile rag run --rm rag-cli access --confirm
```

Том PostgreSQL хранит документы, фрагменты, векторы и тестовые заявки. Для полного стека выполните `docker compose -f backend/compose.yaml --env-file rag/.env up --build`: индексатор дождётся БД, а сервер — завершения индексации. Внутри БД RAG использует отдельную схему `rag`, не затрагивая таблицы Go API. При локальном запуске подключение задаёт `RAG_DATABASE_URL`, по умолчанию `postgresql://buddy:buddy-local@localhost:55432/buddy`. Для переноса прежней базы SQLite повторно выполните `python3 -m rag index` с исходным манифестом.

## Семантический поиск

Путь индексации: документ → существующий chunking по абзацам и предложениям (до 1400 символов, перекрытие 160) → embedding заголовка и текста каждого фрагмента → `vector(1536)` в pgvector. По умолчанию используется `openai/text-embedding-3-small` через OpenAI-совместимый `/embeddings`. Векторы нормализуются; поиск использует косинусное расстояние `<=>` и индекс HNSW. Фильтры роли, команды и текущей версии применяются в SQL до `LIMIT`. Вопрос векторизуется целиком, без словаря синонимов. Существующий system prompt сохранён.

Вызовы embeddings идут пакетами по 32 фрагмента. Повторная индексация неизменившихся версий использует сохранённые векторы. Новый снимок публикуется одной транзакцией после успешного получения всех embeddings: ошибка загрузки, провайдера или БД сохраняет предыдущий снимок. Метаданные, страницы и точные диапазоны цитат хранятся рядом с текстом, а не внутри вектора.

| Переменная | Назначение / значение по умолчанию |
|---|---|
| `RAG_EMBEDDING_API_KEY` | Ключ embedding API; при пустом значении используется `RAG_LLM_API_KEY`. |
| `RAG_EMBEDDING_BASE_URL` | `https://openrouter.ai/api/v1`; задаётся независимо от chat API. |
| `RAG_EMBEDDING_MODEL` | `openai/text-embedding-3-small`. |
| `RAG_EMBEDDING_DIMENSIONS` | `1536`; допустимо 1–2000 при поддержке выбранной моделью. |
| `RAG_MIN_SIMILARITY` | Начальный порог cosine similarity `0.3`; требует настройки по реальным вопросам, включая вопросы без ответа в базе. |

Смена модели или endpoint требует переиндексации: идентификатор embedding-пространства включён в версию документа, несовместимые векторы не смешиваются. При смене размерности создайте новую БД и укажите её в `RAG_DATABASE_URL` перед индексацией. Прямой OpenAI API также поддерживается: укажите `https://api.openai.com/v1`, модель `text-embedding-3-small` и соответствующий ключ.

Предложение `preview_jira_access` определяется актуальной инструкцией `jira.bud.read`, занимающей первое место в поиске и присутствующей в источниках ответа. Подтверждение и проверка права на саму заявку остаются отдельным действием. SQLite-адаптер доступен только при явном создании `SQLiteStore` в тестах; CLI и сервер по умолчанию используют PostgreSQL.

## Работа с моделью

По умолчанию клиент использует OpenRouter, модель `openai/gpt-4o-mini` и OpenAI-совместимый метод `/chat/completions`. Укажите свой ключ в `RAG_LLM_API_KEY`. Для другого провайдера измените `RAG_LLM_BASE_URL` и `RAG_LLM_MODEL`. Внешний сервис получит вопрос, роль, команду и отобранные разрешённые фрагменты. [Документация OpenRouter](https://openrouter.ai/docs/quickstart)

```sh
set -a
. ./rag/.env
set +a
python3 -m rag ask 'Как скачать репозиторий?'
docker compose -f docker-compose.dev.yaml --env-file rag/.env --profile rag run --rm rag-cli ask 'Как скачать репозиторий?'
```

Команда `ask` без chat-ключа или с `--offline` явно помечает режим и показывает выдержку. Поиск всё равно требует embedding API и PostgreSQL. С chat-ключом выполняется один запрос к генеративной модели с проверкой структуры ответа и идентификаторов цитат. Ошибка провайдера отображается как ошибка, повтор возможен по запросу. Известный конфликт, просроченный единственный источник и отсутствие результатов обрабатываются локально.

## База знаний

В [rag/knowledge/manifest.json](knowledge/manifest.json) перечислены источники для индекса. Два листа оригинального чек-листа представлены выдержками о первых неделях. Раздел Playbook представлен кратким пересказом с прямой ссылкой. Рабочие процедуры команд, служебные маркеры и проект BUD — синтетические данные, отмеченные как учебные в выдаче. Условия кейса и ответы вебинара использованы при проектировании. [Чек-лист стажёра](https://docs.google.com/spreadsheets/d/1qKFqOzfSZ2jEz57jBSmq7rukDXi_tRfWohgemViceV0/edit) · [Принципы разработки AvitoTech](https://github.com/avito-tech/playbook/blob/master/development-principles.md)

Чтобы подключить свой набор, положите документы рядом с манифестом и выполните `python3 -m rag index path/to/manifest.json`. Манифест целиком задаёт текущий состав базы. Пропущенные в новой версии документы или отозванные права исчезают из поиска после успешной публикации. Для независимого набора укажите другую БД в `RAG_DATABASE_URL`.

| Поля манифеста | Назначение |
|---|---|
| `id`, `path`, `title` | Постоянный ключ документа, путь внутри каталога манифеста и читаемое название. |
| `source_type`, `origin` | Тип `runbook`, `handbook`, `checklist` или `team_guide`. Происхождение `demo` или `public`. |
| `team`, `roles` | Обязательная область видимости. `*` разрешает все команды в пределах перечисленных ролей. |
| `source_url`, `review_by` | Необязательная исходная ссылка и последний день действия проверки в формате YYYY-MM-DD. Пропущенная дата означает, что срок не задан. |
| `rule_key`, `rule_value` | Необязательная пара для сравнения вариантов одного правила. Значение должно передавать смысл цитируемой инструкции. |

Поддерживаются UTF-8 TXT и Markdown. Для PDF установите `python3 -m pip install -e './rag[pdf]'`. Сканированные страницы дополнительно требуют Tesseract 5 с языками `rus` и `eng`. В Docker этот вариант собирается через `docker build --target pdf -t ai-buddy-rag:pdf ./rag`. Параметр `index --no-ocr` ограничивает извлечение текстовым слоем PDF. Цитата хранит версию, фрагмент, страницу и диапазон символов в извлечённом тексте.

## Вызов из Python

```python
from rag.pipeline import ask
from rag.storage import Audience

result = ask("Как скачать репозиторий?", audience=Audience(role="intern", team="platform"),
             offline=True)
print(result.status, result.answer)
```

В онлайн-режиме передайте `offline=False` и настройте окружение провайдера. Метод синхронный. Из асинхронного обработчика его можно вызывать через `await asyncio.to_thread(ask, ...)`, показывая пользователю ожидание. CLI возвращает структурированный результат по флагу `ask --json`. При интеграции передайте подтверждение отдельному обработчику действия. [Формат результата](../contracts/README.md) · [Спецификация поведения](../docs/rag-sdd.md)

## Тесты и покрытие

```sh
python3 -m unittest discover -s rag/tests -v
python3 -m pip install coverage==6.5.0 -e './rag[pdf]'
python3 -m coverage run -m unittest discover -s rag/tests
python3 -m coverage report
python3 -m coverage xml -o docs/coverage.xml
```

Для интеграционных тестов установите базовые зависимости и задайте `RAG_TEST_DATABASE_URL` — DSN PostgreSQL с pgvector и правом `CREATEDB`. Каждый тест создаёт отдельную временную БД и удаляет только её; внешняя embedding API заменена фиксированными векторами, чтобы проверять SQL, фильтры, транзакции и версии без сетевых расходов:

```sh
RAG_TEST_DATABASE_URL=postgresql://buddy:buddy-local@localhost:55432/buddy \
  python3 -m unittest discover -s rag/tests -p test_vector_storage.py -v
```

Для полного прогона теста OCR нужен Tesseract с английским языком. В минимальном окружении дополнительные проверки PDF помечаются как пропущенные. Покрытие считается по всему пакету `rag`, включая CLI и тестовый адаптер заявок. Целевой порог строго выше 80%. [Отчёт Cobertura](../docs/coverage.xml)
