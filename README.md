# AI-bud

Помощник для адаптации стажёров. В демонстрационном сценарии стажёр Platform получает ошибку 403 в Jira, находит доступную ему инструкцию и отправляет тестовую заявку на чтение проекта BUD. RAG-компонент воспроизводит этот сценарий через командную строку.

| Каталог | Назначение |
|---|---|
| [backend/](backend/) | Каталог для Go API приложения |
| [frontend/](frontend/) | Стартовая веб-страница |
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

После запуска откройте [localhost](http://localhost/). API доступен на
[`localhost:8080`](http://localhost:8080), а RAG индексируется автоматически
перед запуском сервера.

Переменные контейнеров передаются через
[`backend/.env.compose.example`](backend/.env.compose.example) и
[`rag/.env.compose.example`](rag/.env.compose.example) с помощью `env_file` в
[`docker-compose.dev.yaml`](docker-compose.dev.yaml).
Внутри Compose используются имена сервисов `db`, `mock-access` и `rag`, а не
`localhost`.

Остановка:

```sh
docker compose -f docker-compose.dev.yaml down
```

## Стартовая веб-страница

## Сценарий стажёра

Для локального запуска подойдёт Python 3.11+ с SQLite FTS5 и JSON. Из корня репозитория создайте виртуальное окружение, установите пакет и проиндексируйте учебный корпус.

```sh
python3 -m venv .venv
. .venv/bin/activate
python3 -m pip install -e ./rag
python3 -m rag index
python3 -m rag ask 'Не открывается Jira, ошибка 403' --offline
python3 -m rag access
python3 -m rag access --confirm
```

`ask` показывает инструкцию с источником. `access` выводит поля заявки. После подтверждения имитатор возвращает номер и статус `pending`. Повторный вызов `python3 -m rag access` показывает сохранённый результат.

Для запуска через Docker Compose выполните команды ниже. Именованный том сохраняет индекс и заявки между вызовами.

```sh
docker compose -f docker-compose.dev.yaml --profile rag build rag
docker compose -f docker-compose.dev.yaml run --rm rag index
docker compose -f docker-compose.dev.yaml run --rm rag ask 'Не открывается Jira, ошибка 403' --offline
docker compose -f docker-compose.dev.yaml run --rm rag access
docker compose -f docker-compose.dev.yaml run --rm rag access --confirm
```

Настройки LLM, тестовые профили, поддержку PDF, команды тестирования и получения отчёта покрытия см. в [руководстве RAG](rag/README.md). Источники учебного корпуса и их области видимости перечислены в [манифесте](rag/knowledge/manifest.json).
