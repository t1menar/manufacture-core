# ADR-0010. Репозитории и версия контракта

Статус: принято, 2026-11-10.

**Решение.**
- Три репозитория: `manufacture-core` (владелец контракта), `manufacture-sdk-python`, `manufacture-examples`.
- Контракт состоит из `schemas/` (ресурсы v1, конверт v2), `api/openapi.yaml`, `docs/messaging.md` и `testdata/`. Его семвер лежит в `schemas/VERSION`.
- На тег `contract-vX.Y.Z` workflow `contract-release` прикладывает к GitHub Release архив `contract-vX.Y.Z.tar.gz` и `SHA256SUMS`.
- SDK вендорит архив и сверяет его drift-тестом. Модуль сообщает `contract_version` в `module.online`, ядро отклоняет другую мажорную версию.
- e2e живёт в `manufacture-examples` и берёт образ ядра и SDK.

**Последствия.** До 1.0.0 возможны ломающие изменения, о них сообщается в CHANGELOG контракта.
