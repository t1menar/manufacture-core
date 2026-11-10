import { ActionIcon, Badge, Group, Stack, Table, Text, Title, Tooltip } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { api } from "../api/client";
import { q } from "../api/queries";
import type { ModuleStatus } from "../api/types";
import { QueryState } from "../components/QueryState";
import { StatusBadge } from "../components/StatusBadge";
import { formatDuration, formatRate } from "../lib/format";
import { showError } from "../lib/toasts";
import { useLive } from "../live/store";

function Spark({ values }: { values: number[] | undefined }) {
  const v = values ?? [];
  const max = Math.max(0.01, ...v);
  return (
    <svg viewBox="0 0 60 16" width={60} height={16} aria-hidden>
      <polyline fill="none" stroke="#38bdf8" strokeWidth={1} points={v.map((x, i) => `${(i / 59) * 60},${16 - (x / max) * 15}`).join(" ")} />
    </svg>
  );
}

export function ScaleButtons({ m }: { m: ModuleStatus }) {
  const qc = useQueryClient();
  const scale = async (n: number) => {
    try {
      await api.scaleModule(m.name, n);
      void qc.invalidateQueries({ queryKey: ["modules"] });
    } catch (e) {
      showError(e);
    }
  };
  const external = m.runtime === "external";
  return (
    <Group gap={4} wrap="nowrap">
      <Tooltip label={external ? "внешний модуль: реплики запускаются вне ядра" : "меньше реплик"}>
        <ActionIcon size="sm" variant="default" aria-label={`уменьшить ${m.name}`} disabled={external || m.replicas.desired === 0} onClick={() => void scale(m.replicas.desired - 1)}>
          −
        </ActionIcon>
      </Tooltip>
      <Text size="sm" ff="monospace" w={44} ta="center" c={m.replicas.ready < m.replicas.desired ? "orange" : undefined}>
        {m.replicas.ready}/{m.replicas.desired}
      </Text>
      <ActionIcon size="sm" variant="default" aria-label={`увеличить ${m.name}`} disabled={external} onClick={() => void scale(m.replicas.desired + 1)}>
        +
      </ActionIcon>
    </Group>
  );
}

export function ModulesPage() {
  const query = useQuery({ ...q.modules(), refetchInterval: 5000 });
  const live = useLive((s) => s.metrics);
  const hist = useLive((s) => s.rateHistory);
  const liveStatus = useLive((s) => s.modules);
  return (
    <Stack>
      <Title order={3}>Модули</Title>
      <QueryState query={query} empty={(d) => d.length === 0}>
        {(items) => (
          <Table highlightOnHover verticalSpacing="xs" data-testid="modules-table">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Модуль</Table.Th>
                <Table.Th>Состояние</Table.Th>
                <Table.Th>Реплики</Table.Th>
                <Table.Th>Throughput</Table.Th>
                <Table.Th>p50 / p95</Table.Th>
                <Table.Th>Ошибки</Table.Th>
                <Table.Th>В работе</Table.Th>
                <Table.Th>Очередь</Table.Th>
                <Table.Th>DLQ</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {items.map((m0) => {
                const m = liveStatus[m0.name] ?? m0;
                const l = live[m.name] ?? m.live;
                return (
                  <Table.Tr key={m.name}>
                    <Table.Td>
                      <Link to="/modules/$name" params={{ name: m.name }}>
                        <Text fw={600}>{m.name}</Text>
                      </Link>
                      <Text size="xs" c="dimmed" ff="monospace" lineClamp={1} maw={320}>
                        {m.image || "—"}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4}>
                        <StatusBadge status={m.health} />
                        <Badge size="xs" variant="outline" color="gray">
                          {m.driver}
                        </Badge>
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <ScaleButtons m={m} />
                    </Table.Td>
                    <Table.Td>
                      <Group gap={6} wrap="nowrap">
                        <Text size="sm" ff="monospace">
                          {formatRate(l.rate)}
                        </Text>
                        <Spark values={hist[m.name]} />
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" ff="monospace">
                        {formatDuration(l.p50_ms)} / {formatDuration(l.p95_ms)}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c={l.error_rate > 0.1 ? "red" : undefined}>
                        {Math.round(l.error_rate * 100)}%
                      </Text>
                    </Table.Td>
                    <Table.Td>{l.in_flight}</Table.Td>
                    <Table.Td>{l.queue_depth < 0 ? "—" : l.queue_depth}</Table.Td>
                    <Table.Td>{m.dlq_depth ? <Badge color="red">{m.dlq_depth}</Badge> : 0}</Table.Td>
                  </Table.Tr>
                );
              })}
            </Table.Tbody>
          </Table>
        )}
      </QueryState>
    </Stack>
  );
}
