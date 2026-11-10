# testdata

Specs shared by the core tests and, through the contract archive, by the SDKs.

- `valid/` — every file must validate without errors.
- `invalid/schema/` — each file breaks the JSON Schema; any client that vendors `schemas/` must reject it.
- `invalid/rules/` — each file is schema-valid but breaks a semantic rule of `manufacture validate`.

Each invalid file lists the expected diagnostics in comments:

```
# want: <field path> | <message substring>
# warn: <field path> | <message substring>
```

`# warn:` lines declare expected warnings; other warnings are allowed.

## envelope/

- `valid/`, `invalid/` — broker messages that a client must accept or reject (the envelope schema plus blob-ref checks of every `$blob` object in the payload).
- `step_key.json` — `step_key = uuid5(STEP_NS, "<run_id>:<stage>:<step_id>:<item_index or ->:<visit>")` vectors.
- `message_ids.json` — derived message ids (results, `step.waiting` by `seq`, `run.started`, `run.finished`, `run.requested` by dedup key).
