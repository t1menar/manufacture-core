# Паттерн: гейт ревью

Гейт ревью — это обычный шаг модуля, который ждёт человека, плюс переход назад при отказе. Отдельного типа стадии в ядре нет: хватает `wait`, `on_timeout`, `routes` с `max_visits`, истории визитов в CEL и `step_key` (contract 0.3.0, ADR-0014).

## Пайплайн

Файл проверяется в CI: `testdata/valid/review-gate.yaml`.

```yaml
apiVersion: manufacture.dev/v1
kind: Pipeline
metadata: {name: video-review}
spec:
  concurrency: {max_runs: 2, count_waiting: false}
  budget: {run_usd: 5, warn_usd: 4}
  stages:
    - name: scenario
      step:
        id: scenario
        module: module-scenario
        timeout: 20m
        retry: {max_attempts: 3}
        input:
          idea: "data.idea"
          previous: "visit > 1 ? steps.scenario.visits[visit - 1].output : {}"
          comment: "has(steps.review) && has(steps.review.output.comment) ? steps.review.output.comment : ''"
          round: "visit"
    - name: review
      step:
        id: review
        module: module-review
        timeout: 10m
        wait: {timeout: 2d}
        on_timeout: attention
        on_cancel: attention
        merge: none
        input:
          scenario: "steps.scenario.output"
          round: "steps.scenario.visit"
      routes:
        - when: "steps.review.output.decision == 'rejected'"
          goto: scenario
          max_visits: 3
    - name: publish
      step: {module: module-instagram-publisher, timeout: 15m, on_timeout: attention, input: {video: "data.video"}}
---
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: module-review}
spec:
  image: ghcr.io/t1menar/mf-review:1.0.0
  replicas: 2
---
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: module-scenario}
spec:
  image: ghcr.io/t1menar/mf-scenario:1.0.0
  min_budget_usd: 0.5
---
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: module-instagram-publisher}
spec:
```

## Что делает каждая часть

| Часть | Зачем |
|---|---|
| `wait: {timeout: 2d}` | шаг может ждать; lease и `timeout: 10m` на паузе, пока модуль в `waiting`; тикает только `wait.timeout` |
| `on_timeout: attention` | просроченное ревью не ретраится (вторая карточка) и не роняет run: run уходит в `needs_attention`, оператор решает |
| `on_cancel: attention` | отмена шага оператором тоже паркует run, а не проваливает его |
| `merge: none` | решение ревью живёт в `steps.review.output`, в `data` не попадает |
| `routes` + `max_visits: 3` | отказ возвращает run на `scenario` новым визитом; не больше трёх заходов, поэтому третий отказ даёт `needs_attention` (`max_visits_exceeded`). `max_visits` считается на стадию-цель по всем правилам, ведущим в неё |
| `previous`, `comment`, `round` | проекция входа доработки: прошлый выход своей стадии (`visits[visit - 1]`), комментарий ревью, номер захода. На первом визите `previous` = `{}`, `comment` = `''` |
| `count_waiting: false` | run в `waiting_human` не держит слот `max_runs` (по умолчанию) |

## Модуль ревью

```python
@app.step("module-review")
async def review(ctx, inp: ReviewIn) -> ReviewOut:
    # step_key одинаков у всех попыток визита: карточка отправляется один раз
    card = await db.get_or_create_card(ctx.step_key, ctx.attempt_id, inp.scenario, inp.round)
    await telegram.ensure_sent(card)
    await ctx.wait(reason="human", message="ждём ревью в Telegram", expected_by=card.remind_at)
    # обработчик выходит; SDK делает ack после подтверждения step.waiting

# позже, в любой реплике, когда пришёл ответ из Telegram
async def on_telegram_answer(card, decision, comment):
    await app.complete(card.attempt_id, {"decision": decision, "comment": comment})
```

- **Идемпотентность.** Карточка привязана к `step_key`, а не к `attempt.id`: ретрай или переотправка того же визита находит уже отправленную карточку. Новый визит (после отказа) — новый `step_key` и новая карточка.
- **Рестарт реплики.** После `step.waiting` попытка не зависит от реплики: lease не тикает, ответ присылает любая реплика через `app.complete(attempt_id, …)`. Ядро проверяет только, что попытка текущая; поздний ответ на заменённую или отменённую попытку отбрасывается.
- **Напоминания** — забота модуля; `expected_by` только показывается в UI.
- **Отмена.** Если run отменили, все реплики получают `step.cancel`; модуль закрывает карточку.

## Публикация после гейта

Публикатор вызывает `confirm` прямо перед необратимым действием и ведёт ledger по `step_key` (ADR-0013):

```python
@app.step("module-instagram-publisher")
async def publish(ctx, inp):
    if (done := await ledger.get(ctx.step_key)) is not None:
        return done.result                       # тот же визит уже опубликован
    await ctx.confirm(action="media_publish")    # Superseded / Cancelled / DeadlineTooClose → не публикуем
    result = await instagram.publish(inp.video)
    await ledger.put(ctx.step_key, result)
    return result
```

Если попытка после `confirm` пропала (`lost`, таймаут), ядро не ретраит её само: run уходит в `needs_attention` (`outcome_unknown`). Оператор сверяется и делает `POST /api/v1/steps/{step_key}/retry {comment, acknowledge_attention: true}`; новая попытка того же визита находит запись в ledger и не публикует второй раз.

## Что видно оператору

- Run в статусе `waiting_human`, шаг `review` в `waiting` с `message`, `expected_by` и временем ожидания; метрики `mf_runs_waiting`, `mf_steps_waiting`.
- История визитов `scenario` (каждый со своим выходом и снапшотом), `mf_stage_visits_total`.
- Действия: `skip` шага с ручным `output` (решение за ревьюера), `cancel`, `resume` — все с обязательным `comment`.
