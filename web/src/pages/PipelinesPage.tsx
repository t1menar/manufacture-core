import { Badge, Button, Group, Stack, Table, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { q } from "../api/queries";
import { QueryState } from "../components/QueryState";
import { formatDateTime } from "../lib/format";

export function PipelinesPage() {
  const query = useQuery({ ...q.pipelines(), refetchInterval: 5000 });
  return (
    <Stack>
      <Group justify="space-between">
        <Title order={3}>Пайплайны</Title>
        <Button component={Link} to="/pipelines/new">
          Применить YAML
        </Button>
      </Group>
      <QueryState query={query} empty={(d) => d.length === 0}>
        {(items) => (
          <Table highlightOnHover verticalSpacing="sm" data-testid="pipelines-table">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Имя</Table.Th>
                <Table.Th>Ревизия</Table.Th>
                <Table.Th>Стадии</Table.Th>
                <Table.Th>Идут</Table.Th>
                <Table.Th>Очередь</Table.Th>
                <Table.Th>Внимание</Table.Th>
                <Table.Th>Обновлён</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {items.map((p) => (
                <Table.Tr key={p.name}>
                  <Table.Td>
                    <Link to="/pipelines/$name" params={{ name: p.name }}>
                      <Text fw={600}>{p.name}</Text>
                    </Link>
                    {p.managed_by && (
                      <Badge size="xs" variant="outline" ml={6}>
                        {p.managed_by}
                      </Badge>
                    )}
                  </Table.Td>
                  <Table.Td>r{p.revision}</Table.Td>
                  <Table.Td>
                    <Group gap={4}>
                      {p.stages.map((s) => (
                        <Badge key={s.index} size="sm" variant={s.kind === "single" ? "default" : "light"} color={s.kind === "parallel" ? "violet" : s.kind === "foreach" ? "cyan" : undefined} radius="sm">
                          {s.name}
                          {s.kind === "parallel" ? ` ∥${s.lanes.length}` : s.kind === "foreach" ? " ⋔" : ""}
                        </Badge>
                      ))}
                    </Group>
                  </Table.Td>
                  <Table.Td>{p.runs.active}</Table.Td>
                  <Table.Td>{p.runs.queued}</Table.Td>
                  <Table.Td>{p.runs.needs_attention ? <Badge color="orange">{p.runs.needs_attention}</Badge> : 0}</Table.Td>
                  <Table.Td>{formatDateTime(p.updated_at)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </QueryState>
    </Stack>
  );
}
