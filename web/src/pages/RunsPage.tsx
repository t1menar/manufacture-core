import { Button, Group, Select, Stack, Table, Text, TextInput, Title } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useState } from "react";

import { q } from "../api/queries";
import type { RunStatus } from "../api/types";
import { QueryState } from "../components/QueryState";
import { StatusBadge } from "../components/StatusBadge";
import { between, formatDateTime, formatDuration, shortId } from "../lib/format";

const STATUSES: RunStatus[] = ["queued", "running", "succeeded", "failed", "needs_attention", "cancelled"];

export type RunsSearch = { pipeline?: string; status?: RunStatus };

export function RunsPage() {
  const search: RunsSearch = useSearch({ strict: false });
  const navigate = useNavigate();
  const [text, setText] = useState("");
  const [qText] = useDebouncedValue(text, 250);
  const [cursors, setCursors] = useState<string[]>([]);
  const cursor = cursors.at(-1);
  const pipelines = useQuery(q.pipelines());
  const filter = { pipeline: search.pipeline, status: search.status, q: qText || undefined, cursor, limit: 50 };
  const runs = useQuery(q.runs(filter));
  const setFilter = (patch: Partial<RunsSearch>) => {
    setCursors([]);
    void navigate({ to: "/runs", search: { ...search, ...patch } });
  };
  return (
    <Stack>
      <Title order={3}>Запуски</Title>
      <Group>
        <Select placeholder="Все пайплайны" clearable data={(pipelines.data ?? []).map((p) => p.name)} value={search.pipeline ?? null} onChange={(v) => setFilter({ pipeline: v ?? undefined })} w={220} aria-label="Пайплайн" />
        <Select
          placeholder="Любой статус"
          clearable
          data={STATUSES.map((s) => ({ value: s, label: s }))}
          value={search.status ?? null}
          onChange={(v) => setFilter({ status: (v) ?? undefined })}
          w={200}
          aria-label="Статус"
        />
        <TextInput placeholder="run id…" value={text} onChange={(e) => {
          setText(e.currentTarget.value);
          setCursors([]);
        }} w={220} aria-label="Поиск" />
      </Group>
      <QueryState query={runs} empty={(d) => d.items.length === 0}>
        {(d) => (
          <>
            <Table highlightOnHover verticalSpacing="xs" data-testid="runs-table">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Run</Table.Th>
                  <Table.Th>Пайплайн</Table.Th>
                  <Table.Th>Статус</Table.Th>
                  <Table.Th>Стадия</Table.Th>
                  <Table.Th>Запуск</Table.Th>
                  <Table.Th>Создан</Table.Th>
                  <Table.Th>Длительность</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {d.items.map((r) => (
                  <Table.Tr key={r.id}>
                    <Table.Td>
                      <Link to="/runs/$id" params={{ id: r.id }}>
                        <Text ff="monospace" size="sm">
                          {shortId(r.id)}
                        </Text>
                      </Link>
                    </Table.Td>
                    <Table.Td>{r.pipeline}</Table.Td>
                    <Table.Td>
                      <StatusBadge status={r.status} />
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm">
                        {r.stage_index + 1}. {r.stage ?? ""}
                      </Text>
                      {r.error && (
                        <Text size="xs" c="red.4" lineClamp={1} maw={260}>
                          {r.error.code}: {r.error.message}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <Text size="xs">
                        {r.trigger.kind}
                        {r.trigger.by ? ` · ${r.trigger.by}` : ""}
                      </Text>
                    </Table.Td>
                    <Table.Td>{formatDateTime(r.created_at)}</Table.Td>
                    <Table.Td>{formatDuration(between(r.started_at ?? r.created_at, r.finished_at))}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
            <Group justify="flex-end">
              <Button variant="default" size="xs" disabled={cursors.length === 0} onClick={() => setCursors((c) => c.slice(0, -1))}>
                ← Новее
              </Button>
              <Button variant="default" size="xs" disabled={!d.next_cursor} onClick={() => d.next_cursor && setCursors((c) => [...c, d.next_cursor!])}>
                Старше →
              </Button>
            </Group>
          </>
        )}
      </QueryState>
    </Stack>
  );
}
