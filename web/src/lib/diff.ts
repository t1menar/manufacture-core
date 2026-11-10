export type DiffLine = { kind: "same" | "del" | "add"; text: string };

export function diffLines(a: string, b: string): DiffLine[] {
  const A = a.split("\n");
  const B = b.split("\n");
  const n = A.length;
  const m = B.length;
  const dp: number[][] = Array.from({ length: n + 1 }, () => Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i]![j] = A[i] === B[j] ? (dp[i + 1]![j + 1] ?? 0) + 1 : Math.max(dp[i + 1]![j] ?? 0, dp[i]![j + 1] ?? 0);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (A[i] === B[j]) {
      out.push({ kind: "same", text: A[i] ?? "" });
      i += 1;
      j += 1;
    } else if ((dp[i + 1]![j] ?? 0) >= (dp[i]![j + 1] ?? 0)) {
      out.push({ kind: "del", text: A[i] ?? "" });
      i += 1;
    } else {
      out.push({ kind: "add", text: B[j] ?? "" });
      j += 1;
    }
  }
  while (i < n) {
    out.push({ kind: "del", text: A[i] ?? "" });
    i += 1;
  }
  while (j < m) {
    out.push({ kind: "add", text: B[j] ?? "" });
    j += 1;
  }
  return out;
}
