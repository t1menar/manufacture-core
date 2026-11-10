import { describe, expect, it } from "vitest";

import { diffLines } from "./diff";

describe("diffLines", () => {
  it("marks added and removed lines", () => {
    const out = diffLines("a\nb\nc", "a\nx\nc");
    expect(out).toEqual([
      { kind: "same", text: "a" },
      { kind: "del", text: "b" },
      { kind: "add", text: "x" },
      { kind: "same", text: "c" },
    ]);
  });
});
