import { Text } from "@mantine/core";

import { diffLines } from "../lib/diff";

export function DiffView({ before, after, maxHeight = 420 }: { before: string; after: string; maxHeight?: number }) {
  const lines = diffLines(before, after);
  const changed = lines.some((l) => l.kind !== "same");
  if (!changed) return <Text c="dimmed" size="sm">Изменений нет</Text>;
  return (
    <div className="mf-diff" style={{ maxHeight }} data-testid="diff">
      {lines.map((l, i) => (
        <div key={i} className={l.kind === "add" ? "mf-diff-add" : l.kind === "del" ? "mf-diff-del" : undefined}>
          {l.kind === "add" ? "+ " : l.kind === "del" ? "- " : "  "}
          {l.text}
        </div>
      ))}
    </div>
  );
}
