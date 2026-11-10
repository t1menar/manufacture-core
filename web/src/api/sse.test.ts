import { describe, expect, it } from "vitest";

import { decodeEvent, SSEParser } from "./sse";

describe("SSEParser", () => {
  it("parses multi-line data, ids, events and comments across chunk boundaries", () => {
    const p = new SSEParser();
    expect(p.push(": ping\n\nid: 7\nevent: run.created\nda")).toEqual([]);
    const out = p.push('ta: {"a":\ndata: 1}\n\nevent: metrics.tick\r\ndata: {}\r\n\r\n');
    expect(out).toEqual([
      { id: "7", event: "run.created", data: '{"a":\n1}' },
      { id: "7", event: "metrics.tick", data: "{}" },
    ]);
  });

  it("handles a CRLF split between chunks and retry fields", () => {
    const p = new SSEParser();
    expect(p.push("retry: 3000\r")).toEqual([]);
    expect(p.push("\ndata: x\r\n\r\n")).toEqual([{ event: "message", data: "x", retry: 3000 }]);
  });

  it("ignores blank frames without data", () => {
    const p = new SSEParser();
    expect(p.push("event: foo\n\n")).toEqual([]);
  });
});

describe("decodeEvent", () => {
  it("decodes known types and drops unknown or malformed ones", () => {
    expect(decodeEvent({ event: "run.created", data: '{"run_id":"r1"}', id: "1" })).toEqual({
      type: "run.created",
      id: "1",
      data: { run_id: "r1" },
    });
    expect(decodeEvent({ event: "weird", data: "{}" })).toBeNull();
    expect(decodeEvent({ event: "run.created", data: "{oops" })).toBeNull();
  });
});
