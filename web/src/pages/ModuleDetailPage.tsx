import { Badge, Card, Code, Group, SegmentedControl, SimpleGrid, Stack, Table, Tabs, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { useState } from "react";

import { q } from "../api/queries";
import { JsonView } from "../components/JsonView";
import { MetricChart } from "../components/MetricChart";
import { QueryState } from "../components/QueryState";
import { StatusBadge } from "../components/StatusBadge";
import { formatDateTime } from "../lib/format";
import { useLive } from "../live/store";
import { ScaleButtons } from "./ModulesPage";

const RANGES = ["15m", "1h", "6h", "24h", "7d"] as const;

function Charts({ name }: { name: string }) {
  const [range, setRange] = useState<string>("1h");
  const m = useQuery(q.moduleMetrics(name, range));
  return (
    <Stack>
      <SegmentedControl value={range} onChange={setRange} data={[...RANGES]} w="fit-content" size="xs" />
      <QueryState query={m}>
        {(d) => (
          <SimpleGrid cols={{ base: 1, md: 2 }}>
            <MetricChart title="Throughput" unit="/с" series={[{ name: "throughput", label: "шагов", color: "cyan.5", points: d.series.throughput }]} />
            <MetricChart
              title="Latency"
              unit=" мс"
              series={[
                { name: "p50", label: "p50", color: "teal.5", points: d.series.latency_p50_ms },
                { name: "p95", label: "p95", color: "violet.4", points: d.series.latency_p95_ms },
              ]}
            />
            <MetricChart title="Ошибки" unit="/с" series={[{ name: "errors", label: "ошибок", color: "red.5", points: d.series.errors }]} />
            <MetricChart title="В работе" series={[{ name: "in_flight", label: "шагов", color: "blue.4", points: d.series.in_flight }]} />
            <MetricChart title="Очередь" series={[{ name: "queue_depth", label: "сообщений", color: "orange.5", points: d.series.queue_depth }]} />
          </SimpleGrid>
        )}
      </QueryState>
    </Stack>
  );
}

export function ModuleDetailPage() {
  const { name } = useParams({ from: "/modules/$name" });
  const query = useQuery({ ...q.module(name), refetchInterval: 5000 });
  const live = useLive((s) => s.modules[name]);
  return (
    <QueryState query={query}>
      {(m0) => {
        const m = live ? { ...m0, ...live } : m0;
        return (
          <Stack>
            <Group justify="space-between">
              <Group>
                <Title order={3}>{m.name}</Title>
                <StatusBadge status={m.health} size="lg" />
                <Badge variant="outline" color="gray">
                  {m.driver} · r{m.revision}
                </Badge>
              </Group>
              <ScaleButtons m={m} />
            </Group>
            <Text size="sm" ff="monospace" c="dimmed" style={{ wordBreak: "break-all" }}>
              {m.image || "(external)"}
            </Text>
            <Group gap="xs">
              {m.conditions.map((c) => (
                <Badge key={c.type} color={c.status === "True" ? "green" : c.status === "False" ? "red" : "gray"} variant="light" radius="sm">
                  {c.type}
                  {c.reason ? ` · ${c.reason}` : ""}
                </Badge>
              ))}
              {m.sdk_version && <Badge variant="default">SDK {m.sdk_version}</Badge>}
              {m.contract_version && <Badge variant="default">контракт {m.contract_version}</Badge>}
            </Group>
            <Tabs defaultValue="metrics" keepMounted={false}>
              <Tabs.List>
                <Tabs.Tab value="metrics">Метрики</Tabs.Tab>
                <Tabs.Tab value="replicas">Реплики ({m0.replica_list.length})</Tabs.Tab>
                <Tabs.Tab value="spec">Спек</Tabs.Tab>
                <Tabs.Tab value="manifest">Манифест</Tabs.Tab>
              </Tabs.List>
              <Tabs.Panel value="metrics" pt="md">
                <Charts name={name} />
              </Tabs.Panel>
              <Tabs.Panel value="replicas" pt="md">
                <Table>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>Реплика</Table.Th>
                      <Table.Th>Состояние</Table.Th>
                      <Table.Th>SDK</Table.Th>
                      <Table.Th>В работе</Table.Th>
                      <Table.Th>Последний heartbeat</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {m0.replica_list.map((r) => (
                      <Table.Tr key={r.id}>
                        <Table.Td ff="monospace">{r.id}</Table.Td>
                        <Table.Td>
                          <StatusBadge status={r.state} />
                        </Table.Td>
                        <Table.Td>{r.sdk_version ?? "—"}</Table.Td>
                        <Table.Td>{r.in_flight ?? "—"}</Table.Td>
                        <Table.Td>{formatDateTime(r.last_seen_at)}</Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Tabs.Panel>
              <Tabs.Panel value="spec" pt="md">
                <Card withBorder>
                  <Code block>{m0.yaml}</Code>
                  <Text size="xs" c="dimmed" mt="xs">
                    Обновлён {formatDateTime(m0.updated_at)}
                    {m0.applied_by ? ` · ${m0.applied_by}` : ""}
                  </Text>
                </Card>
              </Tabs.Panel>
              <Tabs.Panel value="manifest" pt="md">
                <Card withBorder>{m0.manifest ? <JsonView value={m0.manifest} /> : <Text c="dimmed">Модуль ещё не прислал module.online</Text>}</Card>
              </Tabs.Panel>
            </Tabs>
          </Stack>
        );
      }}
    </QueryState>
  );
}
