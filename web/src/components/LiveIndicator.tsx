import { Group, Text, Tooltip } from "@mantine/core";

import { useLive } from "../live/store";

const LABEL = {
  open: "онлайн",
  connecting: "подключение…",
  reconnecting: "переподключение…",
  unauthorized: "нет доступа",
  closed: "нет связи",
} as const;

export function LiveIndicator() {
  const state = useLive((s) => s.connection);
  return (
    <Tooltip label="Поток событий SSE /api/v1/stream">
      <Group gap={6} wrap="nowrap" data-testid="live-indicator" data-state={state}>
        <span className={`mf-live-dot ${state}`} />
        <Text size="xs" c="dimmed">
          {LABEL[state]}
        </Text>
      </Group>
    </Tooltip>
  );
}
