import { ActionIcon, Anchor, Badge, Group, Text, Tooltip } from "@mantine/core";
import { useState, type ReactNode } from "react";

import { api } from "../api/client";
import { isBlobRef, type BlobRef } from "../api/types";
import { formatBytes } from "../lib/format";
import { showError } from "../lib/toasts";

/** Claim-check reference chip: shows size/type and opens a presigned URL on demand. */
export function BlobChip({ blob }: { blob: BlobRef }) {
  const [busy, setBusy] = useState(false);
  const open = async () => {
    setBusy(true);
    try {
      const p = await api.presign(blob.$blob);
      window.open(p.url, "_blank", "noopener,noreferrer");
    } catch (e) {
      showError(e);
    } finally {
      setBusy(false);
    }
  };
  const file = blob.$blob.split("/").pop() ?? blob.$blob;
  return (
    <Group gap={6} wrap="nowrap" component="span" display="inline-flex" data-testid="blob-ref">
      <Badge variant="outline" color="grape" radius="sm" tt="none">
        $blob
      </Badge>
      <Tooltip label={blob.$blob} withinPortal>
        <Anchor component="button" type="button" size="xs" ff="monospace" onClick={() => void open()} disabled={busy}>
          {file}
        </Anchor>
      </Tooltip>
      <Text span size="xs" c="dimmed">
        {formatBytes(blob.size)}
        {blob.content_type ? ` · ${blob.content_type}` : ""}
      </Text>
    </Group>
  );
}

function Node({ name, value, depth }: { name?: string; value: unknown; depth: number }): ReactNode {
  const [open, setOpen] = useState(depth < 2);
  const key = name !== undefined ? <span className="mf-json-key">{JSON.stringify(name)}: </span> : null;
  if (isBlobRef(value)) {
    return (
      <div style={{ paddingLeft: depth ? 14 : 0 }}>
        {key}
        <BlobChip blob={value} />
      </div>
    );
  }
  if (value !== null && typeof value === "object") {
    const entries = Array.isArray(value) ? value.map((v, i) => [String(i), v] as const) : Object.entries(value as Record<string, unknown>);
    const [l, r] = Array.isArray(value) ? ["[", "]"] : ["{", "}"];
    return (
      <div style={{ paddingLeft: depth ? 14 : 0 }}>
        <ActionIcon size="xs" variant="subtle" onClick={() => setOpen((o) => !o)} aria-label={open ? "Свернуть" : "Развернуть"} mr={2}>
          {open ? "▾" : "▸"}
        </ActionIcon>
        {key}
        {l}
        {!open && <Text span c="dimmed" size="xs">{` ${entries.length} … `}</Text>}
        {open && entries.map(([k, v]: readonly [string, unknown]) => <Node key={k} name={Array.isArray(value) ? undefined : k} value={v} depth={depth + 1} />)}
        {r}
      </div>
    );
  }
  const cls = typeof value === "string" ? "mf-json-str" : typeof value === "number" ? "mf-json-num" : "mf-json-lit";
  return (
    <div style={{ paddingLeft: depth ? 14 : 0 }}>
      {key}
      <span className={cls}>{JSON.stringify(value)}</span>
    </div>
  );
}

/** Collapsible JSON tree that renders `{$blob: s3://…}` values as blob chips. */
export function JsonView({ value }: { value: unknown }) {
  return (
    <div className="mf-json" data-testid="json-view">
      <Node value={value} depth={0} />
    </div>
  );
}
