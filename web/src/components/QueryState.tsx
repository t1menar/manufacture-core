import { Alert, Button, Center, Loader, Stack, Text } from "@mantine/core";
import type { UseQueryResult } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { ProblemError } from "../api/problem";

/** Renders loading / error states for a query and the children once data is there. */
export function QueryState<T>({ query, children, empty }: { query: UseQueryResult<T>; children: (data: T) => ReactNode; empty?: (data: T) => boolean }) {
  if (query.isPending) {
    return (
      <Center py="xl">
        <Loader size="sm" />
      </Center>
    );
  }
  if (query.isError) {
    const e = query.error;
    const p = e instanceof ProblemError ? e.problem : { title: e.message };
    return (
      <Alert color="red" title={p.title ?? "Ошибка"} variant="light">
        <Stack gap="xs">
          {p.detail && <Text size="sm">{p.detail}</Text>}
          <Button size="xs" variant="light" color="red" w="fit-content" onClick={() => void query.refetch()}>
            Повторить
          </Button>
        </Stack>
      </Alert>
    );
  }
  if (empty?.(query.data)) {
    return (
      <Text c="dimmed" ta="center" py="xl">
        Пока пусто
      </Text>
    );
  }
  return <>{children(query.data)}</>;
}
