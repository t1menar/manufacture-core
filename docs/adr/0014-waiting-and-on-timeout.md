# ADR-0014. Ожидание (`step.waiting`, `waiting_human`) и `on_timeout`

Статус: принято, 2026-11-10 (contract 0.3.0, требования R1, R2). Дополняет ADR-0003.

**Контекст.** В v1 ревью было «долгим шагом» с `timeout: 7d`: оно держало слот `max_runs` и prefetch-слот брокера, heartbeat шёл днями, таймаут вёл к авто-retry и второй карточке ревью, а UI не отличал «ждём человека» от «модуль завис».

**Решение.**
- Модуль сообщает `step.waiting {reason: human|external, seq, message?, expected_by?, usage?}`. Попытка и шаг переходят в `waiting`; lease и таймаут исполнения на паузе; тикает только `wait.timeout` шага (по умолчанию 7d). Любой `step.heartbeat` возвращает попытку в `running`; результат можно прислать сразу из `waiting`.
- Run — `waiting_human`, если все незавершённые шаги текущей стадии ждут с `reason=human`. Такой run не занимает слот `max_runs` (`concurrency.count_waiting: false` по умолчанию); ожившему run повторный admission не нужен, временное превышение `max_runs` допустимо.
- SDK делает ack work-сообщения после подтверждения `step.waiting` и позже завершает попытку из любой реплики (`app.complete(attempt_id, …)`). Ядро проверяет только, что попытка текущая. `message_id = uuid5(MSG_NS, "<attempt_id>:step.waiting:<seq>")`.
- `wait` разрешён только у стадии из одного шага.
- Таймаут исполнения считается от первого `step.heartbeat` (SDK шлёт его при получении), а не от диспетчеризации; `deadline` в `step.requested` — необязательный абсолютный потолок.
- `on_timeout: retry | fail | attention` (по умолчанию `retry`) действует на оба таймаута. Перед действием ядро шлёт `step.cancel {reason: timeout}`. `retry` — новая попытка того же визита (тот же `step_key`), по исчерпании `retry.max_attempts` run → `needs_attention`. Подтверждённая попытка (ADR-0013) не ретраится: `needs_attention` (`outcome_unknown`). Валидатор предупреждает о шаге с `wait` без `on_timeout` и о `retry` при `max_attempts: 1`.
- `on_cancel: fail | attention` (по умолчанию `attention`) — что делает с run отмена шага оператором.

**Альтернативы.** Отдельный вид стадии `gate` в ядре (ядро знало бы про людей и каналы; модуль ревью всё равно нужен); оставить долгий шаг (проблемы выше).

**Последствия.** Новые статусы `waiting` (шаг, попытка) и `waiting_human` (run) в API и SSE (`step.waiting`); колонки `waiting*`, `wait_total`, `wait_deadline` в `0002_engine.sql`; метрики `mf_runs_waiting`, `mf_steps_waiting`, `mf_step_wait_duration_seconds`, `mf_step_timeouts_total{kind,action}`. Паттерн — `docs/patterns/review-gate.md`.
