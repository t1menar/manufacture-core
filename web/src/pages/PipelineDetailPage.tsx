import { Badge, Button, Group, Select, SimpleGrid, Stack, Table, Tabs, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { useMemo, useState } from "react";

import { api } from "../api/client";
import { q } from "../api/queries";
import { DiffView } from "../components/DiffView";
import { QueryState } from "../components/QueryState";
import { SpecEditor } from "../components/SpecEditor";
import { formatDateTime } from "../lib/format";
import { showError, showOk } from "../lib/toasts";
import { useLive } from "../live/store";
import { Conveyor2D } from "../scene/Conveyor2D";
import { buildScene } from "../scene/model";

function Revisions({ name, current }: { name: string; current: string }) {
  const revs = useQuery(q.revisions(name));
  const [sel, setSel] = useState<string | null>(null);
  const picked = useQuery({ ...q.revision(name, Number(sel)), enabled: sel !== null });
  return (
    <QueryState query={revs}>
      {(items) => (
        <SimpleGrid cols={{ base: 1, lg: 2 }}>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Ревизия</Table.Th>
                <Table.Th>Когда</Table.Th>
                <Table.Th>Кто</Table.Th>
                <Table.Th>Источник</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {items.map((r) => (
                <Table.Tr key={r.revision} onClick={() => setSel(String(r.revision))} style={{ cursor: "pointer" }} bg={sel === String(r.revision) ? "var(--mantine-color-default-hover)" : undefined}>
                  <Table.Td>
                    r{r.revision} {r.current && <Badge size="xs">текущая</Badge>}
                  </Table.Td>
                  <Table.Td>{formatDateTime(r.applied_at)}</Table.Td>
                  <Table.Td>{r.applied_by}</Table.Td>
                  <Table.Td>{r.source}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
          <div>
            <Select label="Сравнить с текущей" data={items.map((r) => String(r.revision))} value={sel} onChange={setSel} placeholder="ревизия" mb="sm" w={160} />
            {picked.data && <DiffView before={picked.data.yaml} after={current} />}
          </div>
        </SimpleGrid>
      )}
    </QueryState>
  );
}

export function PipelineDetailPage() {
  const { name } = useParams({ from: "/pipelines/$name" });
  const query = useQuery(q.pipeline(name));
  const modules = useLive((s) => s.modules);
  const metrics = useLive((s) => s.metrics);
  const navigate = useNavigate();
  const scene = useMemo(
    () => (query.data ? buildScene({ name, revision: query.data.revision, stages: query.data.stages, modules, metrics, items: {} }) : null),
    [query.data, modules, metrics, name],
  );
  const start = async () => {
    try {
      const r = await api.startRun(name);
      showOk(`Запуск ${r.id.slice(0, 8)}`);
      void navigate({ to: "/runs/$id", params: { id: r.id } });
    } catch (e) {
      showError(e);
    }
  };
  return (
    <QueryState query={query}>
      {(p) => (
        <Stack>
          <Group justify="space-between">
            <Group>
              <Title order={3}>{p.name}</Title>
              <Badge variant="light">r{p.revision}</Badge>
              <Text size="sm" c="dimmed">
                {formatDateTime(p.updated_at)} · {p.applied_by} · {p.source}
              </Text>
            </Group>
            <Group>
              <Button variant="default" onClick={() => void navigate({ to: "/", search: { pipeline: p.name } })}>
                На конвейере
              </Button>
              <Button onClick={() => void start()}>▶ Запустить</Button>
            </Group>
          </Group>
          <Tabs defaultValue="yaml" keepMounted={false}>
            <Tabs.List>
              <Tabs.Tab value="yaml">YAML</Tabs.Tab>
              <Tabs.Tab value="scheme">Схема</Tabs.Tab>
              <Tabs.Tab value="revisions">Ревизии</Tabs.Tab>
            </Tabs.List>
            <Tabs.Panel value="yaml" pt="md">
              <SpecEditor key={`${p.name}@${p.revision}`} initial={p.yaml} baseline={p.yaml} readOnly={p.managed_by === "git"} onApplied={() => void query.refetch()} />
            </Tabs.Panel>
            <Tabs.Panel value="scheme" pt="md">
              <div className="mf-scene" style={{ height: 420 }}>
                {scene && <Conveyor2D scene={scene} animate />}
              </div>
            </Tabs.Panel>
            <Tabs.Panel value="revisions" pt="md">
              <Revisions name={p.name} current={p.yaml} />
            </Tabs.Panel>
          </Tabs>
        </Stack>
      )}
    </QueryState>
  );
}

const TEMPLATE = `apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: my-module}
spec: {image: localhost:5001/my-module:0.1.0, replicas: 1}
---
apiVersion: manufacture.dev/v1
kind: Pipeline
metadata: {name: my-pipeline}
spec:
  stages:
    - name: first
      step: {module: my-module, timeout: 30s}
`;

export function ApplyPage() {
  const navigate = useNavigate();
  return (
    <Stack>
      <Title order={3}>Применить YAML</Title>
      <Text size="sm" c="dimmed">
        Модули и пайплайны в одном файле (через <code>---</code>), как для <code>mfctl apply -f</code>. Повторное применение того же спека ничего не меняет.
      </Text>
      <SpecEditor
        initial={TEMPLATE}
        baseline=""
        onApplied={(r) => {
          const p = r.results.find((x) => x.kind === "Pipeline");
          if (p) void navigate({ to: "/pipelines/$name", params: { name: p.name } });
        }}
      />
    </Stack>
  );
}
