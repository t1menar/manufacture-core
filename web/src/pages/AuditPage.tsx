import { Code, Stack, Table, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";

import { q } from "../api/queries";
import { QueryState } from "../components/QueryState";
import { formatDateTime } from "../lib/format";

export function AuditPage() {
  const query = useQuery(q.audit());
  return (
    <Stack>
      <Title order={3}>Журнал действий</Title>
      <QueryState query={query} empty={(d) => d.items.length === 0}>
        {(d) => (
          <Table verticalSpacing="xs">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Когда</Table.Th>
                <Table.Th>Кто</Table.Th>
                <Table.Th>Действие</Table.Th>
                <Table.Th>Объект</Table.Th>
                <Table.Th>Детали</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {d.items.map((e) => (
                <Table.Tr key={e.id}>
                  <Table.Td>{formatDateTime(e.at)}</Table.Td>
                  <Table.Td>{e.actor}</Table.Td>
                  <Table.Td>
                    <Text ff="monospace" size="sm">
                      {e.action}
                    </Text>
                  </Table.Td>
                  <Table.Td>{e.target}</Table.Td>
                  <Table.Td>{e.details ? <Code>{JSON.stringify(e.details)}</Code> : null}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </QueryState>
    </Stack>
  );
}
