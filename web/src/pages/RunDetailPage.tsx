import { Alert, Badge, Button, Card, Group, SegmentedControl, SimpleGrid, Stack, Table, Text, Title } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useEffect, useState } from "react";

import { api, type RunAction } from "../api/client";
import { q } from "../api/queries";
import { JsonView } from "../components/JsonView";
import { QueryState } from "../components/QueryState";
import { StatusBadge } from "../components/StatusBadge";
import { StepTimeline } from "../components/StepTimeline";
import { between, formatDateTime, formatDuration } from "../lib/format";
import { showError, showOk } from "../lib/toasts";

function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), 500);
    return () => clearInterval(t);
  }, [active]);
  return now;
}

function ContextView({ id, current, snapshots }: { id: string; current: unknown; snapshots: { after_stage: number; created_at: string; size: number }[] }) {
  const [sel, setSel] = useState("current");
  const snap = useQuery({ ...q.snapshot(id, Number(sel)), enabled: sel !== "current" });
  return (
    <Stack gap="xs">
      <SegmentedControl
        size="xs"
        value={sel}
        onChange={setSel}
        data={[{ value: "current", label: "сейчас" }, ...snapshots.map((s) => ({ value: String(s.after_stage), label: `после ${s.after_stage + 1}` }))]}
        w="fit-content"
      />
      {sel === "current" ? <JsonView value={current} /> : <QueryState query={snap}>{(d) => <JsonView value={d} />}</QueryState>}
    </Stack>
  );
}

export function RunDetailPage() {
  const { id } = useParams({ from: "/runs/$id" });
  const query = useQuery(q.run(id));
  const qc = useQueryClient();
  const navigate = useNavigate();
  const status = query.data?.run.status;
  const now = useNow(status === "running" || status === "queued");

  const act = async (a: RunAction) => {
    try {
      const r = await api.runAction(id, a);
      void qc.invalidateQueries({ queryKey: q.run(id).queryKey });
      if (a === "retry" && r.id !== id) {
        showOk(`Новый запуск ${r.id.slice(0, 8)}`);
        void navigate({ to: "/runs/$id", params: { id: r.id } });
      } else showOk(`${a}: ${r.status}`);
    } catch (e) {
      showError(e);
    }
  };

  return (
    <QueryState query={query}>
      {({ run, steps, context, snapshots }) => (
        <Stack>
          <Group justify="space-between">
            <Group>
              <Title order={3} ff="monospace">
                {run.id.slice(0, 8)}
              </Title>
              <StatusBadge status={run.status} size="lg" />
              <Link to="/pipelines/$name" params={{ name: run.pipeline }}>
                <Badge variant="light">
                  {run.pipeline} · r{run.revision}
                </Badge>
              </Link>
            </Group>
            <Group gap="xs">
              {run.status === "needs_attention" && <Button onClick={() => void act("resume")}>Продолжить</Button>}
              {(run.status === "failed" || run.status === "cancelled") && <Button onClick={() => void act("retry")}>Повторить</Button>}
              {(run.status === "running" || run.status === "queued" || run.status === "needs_attention") && (
                <Button color="red" variant="light" onClick={() => void act("cancel")}>
                  Отменить
                </Button>
              )}
            </Group>
          </Group>
          {run.error && (
            <Alert color={run.status === "needs_attention" ? "orange" : "red"} title={`${run.error.class} · ${run.error.code}`}>
              {run.error.message}
            </Alert>
          )}
          {run.attention && <Alert color="orange">{run.attention}</Alert>}
          <SimpleGrid cols={{ base: 2, md: 4 }}>
            <Card withBorder p="xs">
              <Text size="xs" c="dimmed">создан</Text>
              <Text size="sm">{formatDateTime(run.created_at)}</Text>
            </Card>
            <Card withBorder p="xs">
              <Text size="xs" c="dimmed">длительность</Text>
              <Text size="sm">{formatDuration(between(run.started_at ?? run.created_at, run.finished_at, now))}</Text>
            </Card>
            <Card withBorder p="xs">
              <Text size="xs" c="dimmed">стадия</Text>
              <Text size="sm">
                {run.stage_index + 1}. {run.stage}
              </Text>
            </Card>
            <Card withBorder p="xs">
              <Text size="xs" c="dimmed">запуск</Text>
              <Text size="sm">
                {run.trigger.kind}
                {run.trigger.by ? ` · ${run.trigger.by}` : ""}
              </Text>
            </Card>
          </SimpleGrid>
          <Card withBorder>
            <Text fw={600} mb="sm">
              Таймлайн шагов
            </Text>
            <StepTimeline steps={steps} t0={Date.parse(run.started_at ?? run.created_at)} now={run.finished_at ? Date.parse(run.finished_at) : now} />
          </Card>
          <SimpleGrid cols={{ base: 1, lg: 2 }}>
            <Card withBorder>
              <Text fw={600} mb="sm">
                Шаги и попытки
              </Text>
              <Table verticalSpacing={4} fz="xs">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Шаг</Table.Th>
                    <Table.Th>Модуль</Table.Th>
                    <Table.Th>Статус</Table.Th>
                    <Table.Th>Попытки</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {steps.map((s, i) => (
                    <Table.Tr key={i}>
                      <Table.Td ff="monospace">
                        {s.stage_index + 1}. {s.step_id}
                        {s.item_index !== undefined ? `[${s.item_index}]` : ""}
                      </Table.Td>
                      <Table.Td>
                        <Link to="/modules/$name" params={{ name: s.module }}>
                          {s.module}
                        </Link>
                      </Table.Td>
                      <Table.Td>
                        <StatusBadge status={s.status} size="xs" />
                      </Table.Td>
                      <Table.Td>
                        {s.attempts.map((a) => (
                          <Text key={a.attempt_id} size="xs" c={a.error ? "red.4" : "dimmed"}>
                            #{a.number} {a.status}
                            {a.error ? ` — ${a.error.class}/${a.error.code}: ${a.error.message}` : ""}
                          </Text>
                        ))}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Card>
            <Card withBorder>
              <Text fw={600} mb="sm">
                Контекст
              </Text>
              <ContextView id={run.id} current={context} snapshots={snapshots} />
            </Card>
          </SimpleGrid>
        </Stack>
      )}
    </QueryState>
  );
}
