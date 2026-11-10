import { notifications } from "@mantine/notifications";

import { ProblemError } from "../api/problem";

export function showError(e: unknown, title?: string): void {
  const msg = e instanceof ProblemError ? e.message : e instanceof Error ? e.message : String(e);
  notifications.show({ color: "red", title: title ?? "Ошибка", message: msg, autoClose: 7000 });
}

export function showOk(message: string, title = "Готово"): void {
  notifications.show({ color: "green", title, message, autoClose: 3500 });
}
