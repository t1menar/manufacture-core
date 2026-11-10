# ADR-0005. Транспорт: RabbitMQ в обе стороны

Статус: принято, 2026-11-10.

**Решение.**
- Команды (`step.requested`, `source.trigger`) идут в quorum-очередь модуля `mf.module.<name>`, реплики конкурируют за сообщения, prefetch равен concurrency.
- Отмена (`step.cancel`) уходит каждой реплике через topic-exchange `mf.control`.
- Результаты и события модули публикуют в `mf.events`, ядро читает их из `mf.core.events`.
- HTTP-callbacks v1 убраны: модулю нужны только брокер и S3. Исключение (contract 0.3.0) — синхронный `confirm` перед необратимым действием, единственный HTTP-вызов модуля к ядру (ADR-0013).
- Уведомления о run (`run.started`, `run.finished`) идут подписанным модулям в отдельную quorum-очередь `mf.notify.<name>` (ключ `notify.<name>` в `mf.commands`), чтобы не стоять за длинными шагами.
- Ядро пишет команды через transactional outbox, события дедуплицирует через inbox.

**Альтернативы.**
- NATS JetStream: равноценен, но код v1 (outbox relay, DLX, confirms) уже есть для RabbitMQ.
- gRPC: lease, ретраи и балансировку пришлось бы писать самим.
- Kafka: не про очереди задач.

**Последствия.** Шина скрыта за интерфейсом, поэтому её можно заменить. Подробности в `docs/messaging.md`.
