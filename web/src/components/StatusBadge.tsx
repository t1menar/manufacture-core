import { Badge, type BadgeProps } from "@mantine/core";

import { statusColor, statusLabel } from "../lib/format";

export function StatusBadge({ status, ...rest }: { status: string | undefined } & Omit<BadgeProps, "color" | "children">) {
  return (
    <Badge color={statusColor(status)} variant="light" radius="sm" {...rest}>
      {statusLabel(status)}
    </Badge>
  );
}
