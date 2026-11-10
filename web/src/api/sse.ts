import { getToken } from "../auth/token";
import { buildUrl, getTransport } from "./http";
import type { StreamEvent, StreamEventType } from "./types";

export type RawSSE = { id?: string; event: string; data: string; retry?: number };

/**
 * Incremental text/event-stream parser (WHATWG HTML §9.2).
 * Feed it decoded chunks; it returns the complete events in order and keeps the tail.
 */
export class SSEParser {
  private buf = "";
  private data: string[] = [];
  private event = "";
  private id: string | undefined;
  private retry: number | undefined;

  push(chunk: string): RawSSE[] {
    this.buf += chunk;
    const out: RawSSE[] = [];
    for (;;) {
      const m = /\r\n|\r|\n/.exec(this.buf);
      if (!m) break;
      // A lone trailing "\r" may be the first half of "\r\n": wait for more input.
      if (m[0] === "\r" && m.index === this.buf.length - 1) break;
      const line = this.buf.slice(0, m.index);
      this.buf = this.buf.slice(m.index + m[0].length);
      const ev = this.line(line);
      if (ev) out.push(ev);
    }
    return out;
  }

  private line(line: string): RawSSE | null {
    if (line === "") {
      if (this.data.length === 0) {
        this.event = "";
        return null;
      }
      const ev: RawSSE = { event: this.event || "message", data: this.data.join("\n") };
      if (this.id !== undefined) ev.id = this.id;
      if (this.retry !== undefined) ev.retry = this.retry;
      this.data = [];
      this.event = "";
      this.retry = undefined;
      return ev;
    }
    if (line.startsWith(":")) return null;
    const i = line.indexOf(":");
    const field = i < 0 ? line : line.slice(0, i);
    let value = i < 0 ? "" : line.slice(i + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    switch (field) {
      case "data":
        this.data.push(value);
        break;
      case "event":
        this.event = value;
        break;
      case "id":
        if (!value.includes("\0")) this.id = value;
        break;
      case "retry":
        if (/^\d+$/.test(value)) this.retry = Number(value);
        break;
      default:
        break;
    }
    return null;
  }
}

const KNOWN: ReadonlySet<string> = new Set<StreamEventType>([
  "run.created",
  "run.updated",
  "run.finished",
  "step.dispatched",
  "step.started",
  "step.progress",
  "step.succeeded",
  "step.failed",
  "step.skipped",
  "module.status",
  "module.deleted",
  "pipeline.applied",
  "pipeline.deleted",
  "metrics.tick",
  "reset",
]);

/** Turns a raw frame into a typed event; unknown types and malformed JSON are dropped. */
export function decodeEvent(raw: RawSSE): StreamEvent | null {
  if (!KNOWN.has(raw.event)) return null;
  let data: unknown;
  try {
    data = raw.data ? JSON.parse(raw.data) : {};
  } catch {
    return null;
  }
  if (typeof data !== "object" || data === null) return null;
  return { type: raw.event, id: raw.id, data } as StreamEvent;
}

export type StreamState = "connecting" | "open" | "reconnecting" | "unauthorized" | "closed";

export type StreamOptions = {
  topics?: string[];
  onEvent: (e: StreamEvent) => void;
  onState?: (s: StreamState) => void;
  /** Resume point; updated internally from every frame id. */
  lastEventId?: string;
  /** Backoff bounds for reconnects, ms. */
  minDelay?: number;
  maxDelay?: number;
};

/**
 * Connects to `GET /api/v1/stream` with fetch (so `Authorization` and `Last-Event-ID` can be sent)
 * and reconnects with jittered exponential backoff. Returns a function that closes the stream.
 */
export function connectStream(opts: StreamOptions): () => void {
  const topics = opts.topics ?? ["runs", "steps", "modules", "metrics", "resources"];
  let lastId = opts.lastEventId;
  let closed = false;
  let ctrl: AbortController | null = null;
  let attempt = 0;
  let serverRetry: number | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const minDelay = opts.minDelay ?? 500;
  const maxDelay = opts.maxDelay ?? 15_000;
  const setState = (s: StreamState) => opts.onState?.(s);

  const schedule = () => {
    if (closed) return;
    attempt += 1;
    const base = serverRetry ?? Math.min(maxDelay, minDelay * 2 ** (attempt - 1));
    const delay = Math.round(base * (0.75 + Math.random() * 0.5));
    setState("reconnecting");
    timer = setTimeout(() => void run(), delay);
  };

  const run = async () => {
    if (closed) return;
    ctrl = new AbortController();
    setState(attempt === 0 ? "connecting" : "reconnecting");
    const headers = new Headers({ accept: "text/event-stream", "cache-control": "no-cache" });
    const token = getToken();
    if (token) headers.set("authorization", `Bearer ${token}`);
    if (lastId) headers.set("last-event-id", lastId);
    try {
      const res = await getTransport()(
        new Request(buildUrl("/stream", { topics: topics.join(",") }), { headers, signal: ctrl.signal }),
      );
      if (res.status === 401 || res.status === 403) {
        setState("unauthorized");
        return;
      }
      if (!res.ok || !res.body) throw new Error(`stream: HTTP ${res.status}`);
      setState("open");
      attempt = 0;
      const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
      const parser = new SSEParser();
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        for (const raw of parser.push(value)) {
          if (raw.retry !== undefined) serverRetry = raw.retry;
          if (raw.id !== undefined) lastId = raw.id;
          const ev = decodeEvent(raw);
          if (ev) opts.onEvent(ev);
        }
      }
      schedule();
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") return;
      schedule();
    }
  };

  void run();
  return () => {
    closed = true;
    clearTimeout(timer);
    ctrl?.abort();
    setState("closed");
  };
}
