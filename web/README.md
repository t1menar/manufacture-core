# Manufacture admin panel (`web/`)

React 19 + TypeScript (strict) + Vite + Mantine; 3D conveyor on three.js / @react-three/fiber / drei / postprocessing.
Types come from `../api/openapi.yaml` (`npm run gen:api` → `src/api/schema.gen.ts`; CI fails if stale).

## Run

```sh
cd web && npm ci

# 1) demo / dev without a core: in-browser simulator (REST + SSE), token is pre-filled
npm run dev:mock            # http://localhost:5173
#   URL params: ?pipeline=geo-analysis  &view=2d  &docking=0  &warmup=20000  &speed=2  &stress=0  &seed=7

# 2) dev against a running core (Vite proxies /api → MF_CORE_URL, default http://127.0.0.1:8080)
MF_CORE_URL=http://127.0.0.1:8080 npm run dev
#   log in with the core's admin token (MF_ADMIN_TOKEN, or the one `manufacture serve` prints once)

# 3) production: embedded into the Go binary
make -f web/web.mk web      # from the repo root: npm ci && npm run build → web/dist
go build ./cmd/manufacture  # package web embeds dist/, served by web.Handler() at /
```

## Checks

```sh
npm run check               # typecheck + eslint (strictTypeChecked, 0 warnings) + vitest + build
npm run build:mock && npm run e2e -- --project=smoke         # Playwright, headless Chromium (SwiftShader WebGL)
MF_SHOTS_DIR=/tmp/shots npm run shots                        # screenshots of the 3D conveyor and pages
```

## Layout

| Path | What |
|---|---|
| `src/api` | generated schema, typed client, TanStack Query keys, SSE reader (fetch + Last-Event-ID, backoff) |
| `src/live` | pure event reducer + zustand store fed by `/api/v1/stream`, seeded from `/overview` and `/pipelines/{name}/conveyor` |
| `src/scene` | layout (snake rows, lanes for parallel steps), 3D scene (instanced items, module blocks, replicas, effects with auto-degrade), 2D SVG fallback |
| `src/pages` | Конвейер, Пайплайны (CodeMirror YAML + schema/rule validation + diff + dry-run/apply), Модули (metrics charts, scale), Запуски (filters, timeline, context with `$blob` refs, snapshots), Журнал, Настройки |
| `src/mock` | deterministic simulator and mock transport for dev, tests and screenshots |
| `embed.go` | `web.Handler()`: SPA fallback, immutable `/assets/*`, placeholder when the UI is not built |
