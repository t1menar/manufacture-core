# manufacture-core

Ядро Manufacture: универсальный, не привязанный к предметной области движок конвейеров на Go со встроенной админкой. Модули — это Docker-образы на любом языке (для Python есть [SDK](https://github.com/t1menar/manufacture-sdk-python)), пайплайны описываются декларативным YAML. Примеры лежат в [manufacture-examples](https://github.com/t1menar/manufacture-examples).

> Статус: ранняя разработка. Готовы контракт (ресурсы, сообщения, OpenAPI), хранилище ревизий в PostgreSQL, gateway (`apply`, чтение ресурсов, admin-токен) и `mfctl`. Engine, controller и драйверы рантайма появятся следующими шагами (план в [docs/architecture.md](docs/architecture.md) §11).

## Быстрый старт

```bash
make build
bin/manufacture validate -f testdata/valid/hello-world.yaml
bin/manufacture validate -f ./specs/ --strict -o json   # каталог рекурсивно, предупреждения считаются ошибками
cat pipeline.yaml | bin/manufacture validate -f -
```

Пример диагностики:

```
pipeline.yaml:14:26: error: Pipeline/video: spec.stages[1].routes[0].goto: stage "nowhere" is not defined in this pipeline
```

### Ядро и mfctl

```bash
export MF_DATABASE_URL=postgres://mf:mf@127.0.0.1:5432/mf?sslmode=disable
export MF_AMQP_URL=amqp://guest:guest@127.0.0.1:5672/ MF_S3_ENDPOINT=127.0.0.1:9000 MF_S3_ACCESS_KEY=… MF_S3_SECRET_KEY=…
bin/manufacture serve --apply testdata/valid/hello-world.yaml   # миграции, bootstrap-apply, API на 127.0.0.1:8080
```

Без `MF_ADMIN_TOKEN` ядро при первом старте генерирует токен, сохраняет в БД только его SHA-256 и один раз печатает сам токен в stderr.

```bash
export MF_ADMIN_TOKEN=mf_…            # MF_URL по умолчанию http://127.0.0.1:8080
bin/mfctl apply -f specs/             # created / updated / unchanged; повторный apply ревизий не создаёт
bin/mfctl apply -f specs/ --dry-run
bin/mfctl get modules                 # get pipelines | get revisions pipeline/hello-world | -o json|yaml
bin/mfctl describe pipeline hello-world
bin/mfctl scale module hello-hello --replicas 3
bin/mfctl delete pipeline hello-world
```

Невалидный спек отклоняется целиком (HTTP 422, `application/problem+json` с `errors[]`), диагностика та же, что у `validate`, с именами исходных файлов.

Коды выхода: `0` — спек валиден, `1` — найдены ошибки (или предупреждения при `--strict`), `2` — неверные аргументы.

## Ресурсы

```yaml
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: hello-hello}
spec:
  image: localhost:5001/mf-hello-hello:0.1.0
  replicas: 2                  # или {min: 0, max: 5, metric: queue_depth, target: 10}
---
apiVersion: manufacture.dev/v1
kind: Pipeline
metadata: {name: hello-world}
spec:
  trigger: {source: hello-init}
  stages:
    - name: hello
      step: {module: hello-hello, timeout: 30s, retry: {max_attempts: 3}}
    - name: layers                             # параллельные модули на одной стадии
      parallel: [{module: geo-ndvi}, {module: geo-roads}]
    - name: tiles                              # шаг на каждый элемент списка
      foreach: data.tiles
      max_parallel: 8
      step: {module: geo-analyze, input: {tile: item}}
    - name: review
      step: {module: review, timeout: 7d}
      routes:                                  # цикл назад обязан иметь max_visits
        - {when: "steps.review.output.decision == 'rejected'", goto: hello, max_visits: 3}
```

Проверки идут в три слоя:
1. **JSON Schema** (`schemas/v1`): структура, типы, перечисления, форматы имён и длительностей.
2. **Строгое декодирование** в Go-типы: неизвестные поля отклоняются.
3. **Семантика**:
   - ровно одно из `step` и `parallel`, `foreach` только со `step`;
   - `join` и `quorum`;
   - уникальность стадий и `id` шагов;
   - цели `goto` и обязательный `max_visits` для обратных переходов;
   - CEL-выражения с проверкой типа результата;
   - `lease < timeout`, cron, валидность `context.schema`;
   - ссылки на модули.

## Контракт

`schemas/` — источник истины для всех клиентов: SDK держит вендорную копию и проверяет её drift-тестом. Версия контракта лежит в `schemas/VERSION` (сейчас `0.3.0`: до `1.0.0` возможны ломающие изменения; релизы — теги `contract-vX.Y.Z` с архивом схем). Сообщения модулей описаны в `schemas/envelope/v2` и [docs/messaging.md](docs/messaging.md), API gateway — в [api/openapi.yaml](api/openapi.yaml). Каталог `testdata/` разделён на `valid/`, `invalid/schema/` и `invalid/rules/`, и SDK прогоняет его так же, как ядро.

## Конфигурация процесса

Ядро настраивается переменными окружения. `manufacture config` печатает итоговые значения, секреты скрыты.

| Переменная | По умолчанию | |
|---|---|---|
| `MF_LISTEN_ADDR` | `127.0.0.1:8080` | адрес API и админки |
| `MF_DATABASE_URL` | обязательна | `postgres://…` |
| `MF_AMQP_URL` | обязательна | `amqp://…` |
| `MF_S3_ENDPOINT`, `MF_S3_ACCESS_KEY`, `MF_S3_SECRET_KEY` | обязательны | хранилище больших данных |
| `MF_S3_BUCKET`, `MF_S3_TLS` | `mf-blobs`, `false` | |
| `MF_RUNTIME` | `docker` | `docker`, `kubernetes`, `external` |
| `MF_ROLES` | `all` | `engine`, `controller`, `gateway` через запятую |
| `MF_ADMIN_TOKEN` | — | не короче 32 символов; без него токен генерируется при первом старте |
| `MF_SECURE_COOKIES` | `false` | флаг `Secure` у cookie сессии (включить за TLS) |
| `MF_LOG_LEVEL`, `MF_LOG_FORMAT` | `info`, `json` | |

## Структура

```
cmd/manufacture/      ядро: serve | migrate | validate | config | version
cmd/mfctl/            клиент API: apply | get | describe | scale | delete | run | logs
internal/cli/         команды manufacture, вывод, коды выхода
internal/app/         сборка ролей serve из конфигурации
internal/db/          пул pgx, миграции goose (embed), транзакции
internal/store/       ресурсы и ревизии, apply, настройки, аудит
internal/gateway/     REST API, admin-токен и сессии, problem+json
internal/mfctl/       команды mfctl
internal/contract/    конверт сообщений v2, топология RabbitMQ
internal/itest/       интеграционные тесты (MF_TEST_DATABASE_URL)
internal/config/      конфигурация из env
internal/spec/        типы ресурсов, YAML с позициями, валидация
internal/spec/expr/   компиляция CEL
internal/buildinfo/   версия сборки
schemas/              JSON Schema контракта (go:embed)
testdata/             общие валидные и невалидные спеки
```

## Разработка

Нужны Go 1.26+ и [golangci-lint](https://golangci-lint.run) v2.

```bash
make itest-db  # временный PostgreSQL для интеграционных тестов; без MF_TEST_DATABASE_URL они пропускаются
make check     # tidy, lint, test -race, прогон testdata через бинарник
make help
```

## Лицензия

MIT
