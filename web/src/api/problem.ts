/** RFC 9457 problem+json as returned by the gateway. */
export type FieldError = { path: string; line?: number; message: string };

export type Problem = {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
  code?: string;
  request_id?: string;
  /** Spec validation errors (422 from /apply). */
  errors?: FieldError[];
};

export class ProblemError extends Error {
  readonly problem: Problem;
  readonly status: number;

  constructor(problem: Problem, status = problem.status ?? 0) {
    super(formatProblem(problem));
    this.name = "ProblemError";
    this.problem = problem;
    this.status = status;
  }
}

export function formatProblem(p: Problem): string {
  const title = p.title?.trim() ?? "";
  const head = title !== "" ? title : (p.code ?? "Ошибка запроса");
  const extra = [p.detail, p.code && p.code !== head ? p.code : ""].map((s) => (s ?? "").trim()).filter(Boolean);
  return extra.length ? `${head}: ${extra.join(" · ")}` : head;
}

function parseFieldErrors(v: unknown): FieldError[] | undefined {
  if (!Array.isArray(v)) return undefined;
  const out: FieldError[] = [];
  for (const e of v as unknown[]) {
    if (typeof e !== "object" || e === null) continue;
    const o = e as Record<string, unknown>;
    if (typeof o.message !== "string") continue;
    out.push({
      path: typeof o.path === "string" ? o.path : "",
      message: o.message,
      ...(typeof o.line === "number" ? { line: o.line } : {}),
    });
  }
  return out.length ? out : undefined;
}

export function parseProblem(body: unknown, status: number): Problem {
  if (body && typeof body === "object") {
    const o = body as Record<string, unknown>;
    const str = (k: string): string | undefined => {
      const v = o[k];
      return typeof v === "string" && v.length > 0 ? v : undefined;
    };
    const problem: Problem = {
      type: str("type"),
      title: str("title"),
      status: typeof o.status === "number" ? o.status : status,
      detail: str("detail"),
      instance: str("instance"),
      code: str("code"),
      request_id: str("request_id"),
      errors: parseFieldErrors(o.errors),
    };
    if (problem.title || problem.code || problem.detail || problem.errors) return problem;
  }
  return {
    title: "Некорректный ответ сервера",
    status,
    detail: typeof body === "string" ? body.slice(0, 200) : undefined,
  };
}

export async function problemFromResponse(res: Response): Promise<Problem> {
  const ct = res.headers.get("content-type") ?? "";
  try {
    if (ct.includes("json")) return parseProblem(await res.json(), res.status);
    return parseProblem(await res.text(), res.status);
  } catch {
    return { title: res.statusText || "Ошибка сети", status: res.status };
  }
}
