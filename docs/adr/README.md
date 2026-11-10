# ADR

| # | Решение |
|---|---|
| [0001](0001-single-binary-with-roles.md) | Один бинарник с ролями engine, controller и gateway |
| [0002](0002-pluggable-runtime.md) | Подключаемый runtime-драйвер (docker, kubernetes, external) |
| [0003](0003-pipeline-model.md) | Модель пайплайна: стадии, parallel, foreach, routes, max_visits |
| [0004](0004-declarative-config.md) | YAML-спек, БД как хранилище ревизий, единый apply |
| [0005](0005-rabbitmq-transport.md) | RabbitMQ в обе стороны |
| [0006](0006-envelope-v2.md) | Конверт v2, attempt id вместо fence |
| [0007](0007-context-and-blobs.md) | Контекст запуска, merge patch, claim-check `$blob` |
| [0008](0008-observability.md) | Метрики от ядра, OTel, SSE |
| [0009](0009-admin-panel.md) | Админка: React + r3f, embed |
| [0010](0010-repositories-and-contract.md) | Три репозитория, версия и релиз контракта |
| [0011](0011-security-baseline.md) | Один admin-токен, loopback, секреты по ссылке |
| [0012](0012-cel-expressions.md) | CEL для выражений |

Полный дизайн: [../architecture.md](../architecture.md).
