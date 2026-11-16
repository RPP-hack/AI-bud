# AI-bud

Помощник для адаптации стажёров. В демонстрационном сценарии стажёр Platform получает ошибку 403 в Jira, находит доступную ему инструкцию и отправляет тестовую заявку на чтение проекта BUD. RAG-компонент воспроизводит этот сценарий через командную строку.

| Каталог | Назначение |
|---|---|
| [backend/](backend/) | Каталог для Go API приложения |
| [frontend/](frontend/README.md) | React + TypeScript: помощник, источники, заявки и следующие шаги |
| [gateway/](gateway/) | Caddy раздаёт содержимое `frontend/` |
| [rag/](rag/README.md) | Python-пакет, поиск по базе знаний, работа с моделью и CLI |
| [contracts/](contracts/README.md) | Формат ответа RAG и граница ответственности при интеграции |
| [docs/](docs/rag-sdd.md) | Спецификация поведения и план проверки пользы |

Go REST → Python gRPC и внутренний Python REST: [запуск API](docs/rag-api.md).

## Запуск всего проекта

Запустите весь проект из корня репозитория:

```sh
docker compose -f docker-compose.dev.yaml up --build -d
```

Gateway Caddy обслуживает статическую сборку и проксирует REST в существующий Go API. Сборка frontend выполняется отдельной завершающейся задачей, дополнительного HTTP-сервера нет. [Запуск, тестовые профили и проверка сценария](frontend/README.md).
После запуска откройте [localhost](http://localhost/). API доступен на
[`localhost:8080`](http://localhost:8080), а RAG индексируется автоматически
перед запуском сервера.

Для RAG включена `pull_policy: build`: при `docker compose up` образы индексатора
и сервера собираются из текущего кода с использованием кэша сборки. Это исключает
запуск старого индексатора после смены реализации эмбеддингов. Команда `start`
только запускает ранее созданные контейнеры; после обновления кода используйте `up`.

Переменные контейнеров передаются через
[`backend/.env.compose.example`](backend/.env.compose.example) и
[`rag/.env.compose.example`](rag/.env.compose.example) с помощью `env_file` в
[`docker-compose.dev.yaml`](docker-compose.dev.yaml).
Внутри Compose используются имена сервисов `db`, `mock-access` и `rag`, а не
`localhost`.

Поиск использует локальную `BAAI/bge-m3` (1024 измерения) и не требует API-ключа.
При первом запуске индексатор скачивает веса модели (около 2.3 ГБ) в общий том
`rag-models`; сервер загружает их из кэша до прохождения healthcheck.
Настройки можно переопределить в `rag/.env`, созданном из `rag/.env.example`:
файл одинаково применяется к `rag-index`, `rag` и `rag-cli`.
Ключ `RAG_LLM_API_KEY` нужен для генерации через внешнюю LLM в режиме `llm`.

Остановка:

```sh
docker compose -f docker-compose.dev.yaml down
```

## Стартовая веб-страница

## Сценарий стажёра

Для локального запуска нужны Python 3.11+, PostgreSQL с pgvector и зависимости локальной модели. Скопируйте `rag/.env.example` в `rag/.env`. Из корня репозитория:

```sh
python3 -m venv .venv
. .venv/bin/activate
python3 -m pip install -e './rag[local]'
set -a
. ./rag/.env
set +a
docker compose -f docker-compose.dev.yaml --profile rag up -d rag-db
python3 -m rag index
python3 -m rag ask 'Не открывается Jira, ошибка 403' --offline
python3 -m rag access
python3 -m rag access --confirm
```

`ask` показывает инструкцию с источником. `access` выводит поля заявки. После подтверждения имитатор возвращает номер и статус `pending`. Повторный вызов `python3 -m rag access` показывает сохранённый результат.

Для запуска через Docker Compose загрузите переменные из `rag/.env`, как показано выше, и выполните команды ниже. Именованный том PostgreSQL сохраняет документы, embeddings и заявки между вызовами.

```sh
docker compose -f docker-compose.dev.yaml --profile rag build rag-cli
docker compose -f docker-compose.dev.yaml run --rm rag-cli index
docker compose -f docker-compose.dev.yaml run --rm rag-cli ask 'Не открывается Jira, ошибка 403' --offline
docker compose -f docker-compose.dev.yaml run --rm rag-cli access
docker compose -f docker-compose.dev.yaml run --rm rag-cli access --confirm
```

Настройки LLM, тестовые профили, поддержку PDF, команды тестирования и получения отчёта покрытия см. в [руководстве RAG](rag/README.md). Источники учебного корпуса и их области видимости перечислены в [манифесте](rag/knowledge/manifest.json).
