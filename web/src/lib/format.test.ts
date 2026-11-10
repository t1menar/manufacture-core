import { describe, expect, it } from "vitest";

import { between, formatBytes, formatDuration, statusColor, statusLabel } from "./format";

describe("format", () => {
  it("formats durations across units", () => {
    expect(formatDuration(450)).toBe("450 мс");
    expect(formatDuration(4500)).toBe("4.5 с");
    expect(formatDuration(125_000)).toBe("2 мин 5 с");
    expect(formatDuration(3 * 3_600_000 + 60_000)).toBe("3 ч 1 мин");
    expect(formatDuration(undefined)).toBe("—");
  });
  it("formats bytes", () => {
    expect(formatBytes(73_400_320)).toBe("70 МиБ");
    expect(formatBytes(512)).toBe("512 Б");
  });
  it("computes spans and labels statuses", () => {
    expect(between("2026-01-01T00:00:00Z", "2026-01-01T00:00:02Z")).toBe(2000);
    expect(statusLabel("needs_attention")).toBe("нужно внимание");
    expect(statusColor("failed")).toBe("red");
  });
});
