import { describe, expect, it } from "vitest";

import { formatProblem, parseProblem, ProblemError } from "./problem";

describe("parseProblem", () => {
  it("reads RFC 9457 fields and validation errors", () => {
    const p = parseProblem(
      {
        title: "Invalid spec",
        status: 422,
        code: "invalid_spec",
        errors: [{ path: "spec.stages[0].step.module", line: 7, message: "unknown module" }, { bogus: true }],
      },
      422,
    );
    expect(p.code).toBe("invalid_spec");
    expect(p.errors).toEqual([{ path: "spec.stages[0].step.module", line: 7, message: "unknown module" }]);
    expect(formatProblem(p)).toBe("Invalid spec: invalid_spec");
  });

  it("falls back when the body is not a problem", () => {
    const p = parseProblem("oops", 500);
    expect(p.title).toBe("Некорректный ответ сервера");
    expect(p.detail).toBe("oops");
    expect(new ProblemError(p).status).toBe(500);
  });
});
