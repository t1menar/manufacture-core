import type { Transport } from "../api/http";
import { Simulator } from "./simulator";

const json = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": status >= 400 ? "application/problem+json" : "application/json" },
  });

const problem = (status: number, title: string, code: string, detail?: string): Response =>
  json({ type: "about:blank", title, status, code, ...(detail ? { detail } : {}) }, status);

export const MOCK_TOKEN = "mfa_mock_admin_token";

/**
 * In-browser stand-in for the gateway: the same REST + SSE contract, served from the simulator.
 * Any non-empty token except "bad" is accepted, so the login flow can be exercised.
 */
export function createMockTransport(sim: Simulator): Transport {
  return async (req) => {
    const url = new URL(req.url);
    const path = url.pathname.replace(/^\/api\/v1/, "");
    const auth = req.headers.get("authorization") ?? "";
    const token = auth.startsWith("Bearer ") ? auth.slice(7) : "";
    if (!url.pathname.startsWith("/api/v1/")) return problem(404, "Not found", "not_found");
    if (!token || token === "bad") return problem(401, "Нужен admin-токен", "unauthorized", "Передайте Authorization: Bearer <token>");
    const m = (re: RegExp) => re.exec(path);
    const q = url.searchParams;
    let g: RegExpExecArray | null;

    if (req.method === "GET") {
      if (path === "/stream") return stream(sim, req);
      if (path === "/overview") return json(sim.overview());
      if (path === "/system") {
        return json({
          version: "0.3.0-mock",
          commit: "mock",
          contract_version: "0.2.1",
          driver: "docker",
          roles: ["engine", "controller", "gateway"],
          limits: { max_message_bytes: 262_144, inline_max_bytes: 65_536, max_apply_bytes: 1_048_576, max_foreach_items: 10_000 },
        });
      }
      if (path === "/pipelines") return json({ items: sim.pipelineSummaries() });
      if ((g = m(/^\/pipelines\/([^/]+)\/conveyor$/))) return orNotFound(sim.conveyor(decodeURIComponent(g[1]!)));
      if ((g = m(/^\/pipelines\/([^/]+)$/))) return orNotFound(sim.pipelineDetail(decodeURIComponent(g[1]!)));
      if ((g = m(/^\/pipelines\/([^/]+)\/revisions$/))) {
        const r = sim.revisions(decodeURIComponent(g[1]!));
        return orNotFound(r && { items: r });
      }
      if ((g = m(/^\/pipelines\/([^/]+)\/revisions\/(\d+)$/))) return orNotFound(sim.pipelineDetail(decodeURIComponent(g[1]!), Number(g[2])));
      if (path === "/modules") return json({ items: sim.moduleList() });
      if ((g = m(/^\/modules\/([^/]+)$/))) return orNotFound(sim.moduleDetail(decodeURIComponent(g[1]!)));
      if ((g = m(/^\/modules\/([^/]+)\/metrics$/))) return orNotFound(sim.metrics(decodeURIComponent(g[1]!), q.get("range") ?? "1h"));
      if (path === "/runs") {
        return json(sim.listRuns({ pipeline: q.get("pipeline"), status: q.get("status"), q: q.get("q"), cursor: q.get("cursor"), limit: q.has("limit") ? Number(q.get("limit")) : 50 }));
      }
      if ((g = m(/^\/runs\/([^/]+)$/))) return orNotFound(sim.runDetail(decodeURIComponent(g[1]!)));
      if ((g = m(/^\/runs\/([^/]+)\/snapshots\/(\d+)$/))) return orNotFound(sim.snapshot(decodeURIComponent(g[1]!), Number(g[2])));
      if (path === "/blobs/presign") {
        const ref = q.get("ref") ?? "";
        if (!ref.startsWith("s3://")) return problem(400, "Некорректная ссылка", "bad_ref");
        return json({ url: `https://minio.local/${ref.slice(5)}?X-Amz-Signature=mock`, expires_at: new Date(sim.now + 900_000).toISOString() });
      }
    }
    if (req.method === "POST") {
      if (path === "/apply") {
        const r = sim.apply(await req.text(), q.get("dry_run") === "true");
        return json(r.body, r.status);
      }
      if ((g = m(/^\/pipelines\/([^/]+)\/runs$/))) {
        const body = (await req.json().catch(() => ({}))) as { data?: Record<string, unknown> };
        const r = sim.createRun(decodeURIComponent(g[1]!), { kind: "manual", by: "admin" }, body.data);
        return r ? json(r.run, 201) : problem(404, "Пайплайн не найден", "not_found");
      }
      if ((g = m(/^\/modules\/([^/]+)\/scale$/))) {
        const body = (await req.json().catch(() => ({}))) as { replicas?: unknown };
        if (typeof body.replicas !== "number") return problem(400, "replicas must be a number", "bad_request");
        const name = decodeURIComponent(g[1]!);
        return orNotFound(sim.scale(name, body.replicas) && sim.moduleDetail(name));
      }
      if ((g = m(/^\/runs\/([^/]+)\/(cancel|retry|resume)$/))) {
        const r = sim.runAction(decodeURIComponent(g[1]!), g[2]!);
        if (!r) return problem(404, "Запуск не найден", "not_found");
        if ("conflict" in r) return problem(409, "Действие недоступно", "conflict", r.conflict);
        return json(r, g[2] === "retry" ? 201 : 200);
      }
    }
    return problem(404, "Not found", "not_found");
  };
}

function orNotFound(v: unknown): Response {
  return v === null || v === undefined ? problem(404, "Не найдено", "not_found") : json(v);
}

function stream(sim: Simulator, req: Request): Response {
  const enc = new TextEncoder();
  let unsub: (() => void) | undefined;
  let ping: ReturnType<typeof setInterval> | undefined;
  const body = new ReadableStream<Uint8Array>({
    start(ctrl) {
      const send = (s: string) => {
        try {
          ctrl.enqueue(enc.encode(s));
        } catch {
          unsub?.();
        }
      };
      const frame = (id: string, type: string, data: unknown) =>
        send(`${id ? `id: ${id}\n` : ""}event: ${type}\ndata: ${JSON.stringify(data)}\n\n`);
      send("retry: 2000\n\n");
      const pipeline = new URL(req.url).searchParams.get("pipeline");
      const keep = (e: { ev: { type: string; data: unknown } }) => !pipeline || !(e.ev.type.startsWith("run.") || e.ev.type.startsWith("step.")) || (e.ev.data as { pipeline?: string }).pipeline === pipeline;
      const backlog = sim.since(req.headers.get("last-event-id") ?? new URL(req.url).searchParams.get("last_event_id") ?? undefined);
      if (backlog === null) frame(sim.lastEventId, "reset", {});
      else for (const e of backlog) if (keep(e)) frame(e.id, e.ev.type, e.ev.data);
      unsub = sim.subscribe((e) => {
        if (keep(e)) frame(e.id, e.ev.type, e.ev.data);
      });
      ping = setInterval(() => send(": ping\n\n"), 15_000);
      req.signal.addEventListener("abort", () => {
        unsub?.();
        clearInterval(ping);
        try {
          ctrl.close();
        } catch {
          /* already closed */
        }
      });
    },
    cancel() {
      unsub?.();
      clearInterval(ping);
    },
  });
  return new Response(body, { status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache" } });
}
