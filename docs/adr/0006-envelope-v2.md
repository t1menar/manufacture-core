# ADR-0006. Конверт сообщений v2

Статус: принято, 2026-11-10.

**Решение.** `schemas/envelope/v2/envelope.schema.json` с полями `message_id`, `type`, `schema_version: 2`, `occurred_at`, `producer`, `traceparent`, `run_id`, `attempt{id, number}` и `payload`. Схема payload зависит от типа.
- **Fence из v1 заменён уникальным `attempt.id`.** Результат для попытки, которая уже не текущая, ядро игнорирует (метрика `mf_stale_results_total`).
- Модули выводят `message_id` результатов детерминированно: `uuid5(MF_NAMESPACE, "<attempt_id>:<type>")` (см. `docs/messaging.md`). Повтор после обрыва связи дедуплицируется.
- Лимит конверта 256 КиБ, всё большее передаётся ссылкой (ADR-0007).

**Последствия.** Ядро валидирует каждое входящее и исходящее сообщение по схеме. SDK вендорит ту же схему.
