import { getToken } from "../auth/token";
import { ProblemError, problemFromResponse } from "./problem";

/** Anything that turns a Request into a Response: `fetch` in production, the simulator in mock mode and tests. */
export type Transport = (req: Request) => Promise<Response>;

let transport: Transport = (req) => globalThis.fetch(req);

export function setTransport(t: Transport): void {
  transport = t;
}

export function getTransport(): Transport {
  return transport;
}

export const API_PREFIX = "/api/v1";

function baseUrl(): string {
  return typeof window !== "undefined" ? window.location.origin : "http://127.0.0.1";
}

export type RequestOptions = {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  query?: Record<string, string | number | boolean | undefined | null>;
  body?: unknown;
  /** Raw body (e.g. YAML) with its content type; wins over `body`. */
  raw?: { body: string; contentType: string };
  headers?: Record<string, string>;
  signal?: AbortSignal;
};

export function buildUrl(path: string, query?: RequestOptions["query"]): string {
  const url = new URL(path.startsWith("/api/") ? path : `${API_PREFIX}${path}`, baseUrl());
  for (const [k, v] of Object.entries(query ?? {})) {
    if (v !== undefined && v !== null && v !== "") url.searchParams.set(k, String(v));
  }
  return url.toString();
}

export function buildRequest(path: string, opts: RequestOptions = {}): Request {
  const headers = new Headers({ accept: "application/json", ...opts.headers });
  const token = getToken();
  if (token) headers.set("authorization", `Bearer ${token}`);
  let body: string | undefined;
  if (opts.raw) {
    body = opts.raw.body;
    headers.set("content-type", opts.raw.contentType);
  } else if (opts.body !== undefined) {
    body = JSON.stringify(opts.body);
    headers.set("content-type", "application/json");
  }
  return new Request(buildUrl(path, opts.query), {
    method: opts.method ?? (body === undefined ? "GET" : "POST"),
    headers,
    body: body ?? null,
    signal: opts.signal ?? null,
  });
}

/** Performs a request and decodes JSON; non-2xx responses throw ProblemError. */
export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  let res: Response;
  try {
    res = await transport(buildRequest(path, opts));
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e;
    throw new ProblemError({ title: "Ядро недоступно", detail: e instanceof Error ? e.message : String(e), status: 0 }, 0);
  }
  if (!res.ok) throw new ProblemError(await problemFromResponse(res), res.status);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}
