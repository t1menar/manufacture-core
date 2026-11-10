import { Alert, Button, Card, Group, SimpleGrid, Stack, Table, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";

import { q } from "../api/queries";
import { maskToken, setToken, useToken } from "../auth/token";
import { LoginCard } from "../components/LoginCard";
import { QueryState } from "../components/QueryState";
import { formatBytes } from "../lib/format";
import { useLive } from "../live/store";

export function SettingsPage() {
  const token = useToken();
  const system = useQuery({ ...q.system(), enabled: Boolean(token) });
  const clear = useLive((s) => s.clear);
  return (
    <Stack maw={900}>
      <Title order={3}>Настройки</Title>
      <SimpleGrid cols={{ base: 1, md: 2 }}>
        <Card withBorder>
          <Stack>
            <Text fw={600}>Admin-токен</Text>
            {token ? (
              <>
                <Text size="sm">
                  Сохранён в этом браузере: <code data-testid="masked-token">{maskToken(token)}</code>
                </Text>
                <Group>
                  <Button
                    variant="light"
                    color="red"
                    onClick={() => {
                      setToken(null);
                      clear();
                    }}
                  >
                    Выйти
                  </Button>
                </Group>
              </>
            ) : (
              <LoginCard compact />
            )}
            <Text size="xs" c="dimmed">
              Один admin-токен на этом этапе (роли viewer/operator/admin появятся с C12). Токен отправляется как <code>Authorization: Bearer</code> и в API, и в поток событий.
            </Text>
          </Stack>
        </Card>
        <Card withBorder>
          <Text fw={600} mb="sm">
            Ядро
          </Text>
          {token ? (
            <QueryState query={system}>
              {(s) => (
                <Table fz="sm">
                  <Table.Tbody>
                    <Table.Tr>
                      <Table.Td c="dimmed">версия</Table.Td>
                      <Table.Td>
                        {s.version}
                        {s.commit ? ` (${s.commit.slice(0, 7)})` : ""}
                      </Table.Td>
                    </Table.Tr>
                    <Table.Tr>
                      <Table.Td c="dimmed">контракт</Table.Td>
                      <Table.Td>{s.contract_version}</Table.Td>
                    </Table.Tr>
                    <Table.Tr>
                      <Table.Td c="dimmed">драйвер</Table.Td>
                      <Table.Td>{s.driver}</Table.Td>
                    </Table.Tr>
                    <Table.Tr>
                      <Table.Td c="dimmed">роли</Table.Td>
                      <Table.Td>{s.roles.join(", ")}</Table.Td>
                    </Table.Tr>
                    <Table.Tr>
                      <Table.Td c="dimmed">сообщение / inline / apply</Table.Td>
                      <Table.Td>
                        {formatBytes(s.limits.max_message_bytes)} / {formatBytes(s.limits.inline_max_bytes)} / {formatBytes(s.limits.max_apply_bytes)}
                      </Table.Td>
                    </Table.Tr>
                  </Table.Tbody>
                </Table>
              )}
            </QueryState>
          ) : (
            <Text c="dimmed" size="sm">
              Войдите, чтобы увидеть параметры ядра.
            </Text>
          )}
          <Text size="xs" c="dimmed" mt="sm">
            Настройки ядра задаются env-переменными и здесь только читаются.
          </Text>
        </Card>
      </SimpleGrid>
      {__MF_MOCK__ && (
        <Alert color="grape" variant="light" title="Демо-режим">
          Панель работает со встроенным симулятором (<code>npm run dev:mock</code>), запросы к ядру не уходят. Подходит любой токен, кроме <code>bad</code>.
        </Alert>
      )}
    </Stack>
  );
}
