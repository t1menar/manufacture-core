# ADR-0012. CEL для выражений

Статус: принято, 2026-11-10.

**Решение.** `when`, `routes.when`, `foreach` и `input` — выражения CEL (cel.dev/cel-go). Переменные: `data`, `steps`, `run`, `attempt`, `visit`, а в `foreach`-шаге ещё `item` и `index`. С contract 0.3.0 `steps.<id>` содержит `output` (последний визит), `visits` (map по номеру визита с 1: `{visit, step_key, status, output, attempt_id, finished_at, error?}`), `last` и `visit`; `visit` — номер текущего визита исполняемой стадии. Тип `visits` при проверке — `map(int, dyn)`. Выражения компилируются и проверяются по типу результата при validate и apply. Вычисление безопасно: без побочных эффектов и с ограничением стоимости.

**Альтернативы.** JSONPath или JMESPath: нет условий и типов. Lua или JS: исполняемый код в ядре.
