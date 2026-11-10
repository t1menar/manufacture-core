import { Alert, Button, Card, PasswordInput, Stack, Text, Title } from "@mantine/core";
import { useState, type SyntheticEvent } from "react";

import { api } from "../api/client";
import { ProblemError } from "../api/problem";
import { getToken, setToken } from "../auth/token";

/** Admin token login: the token is checked against `/api/v1/system` and kept only in this browser. */
export function LoginCard({ compact = false }: { compact?: boolean }) {
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: SyntheticEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const prev = getToken();
    setToken(value);
    try {
      await api.system();
    } catch (err) {
      setToken(prev);
      setError(err instanceof ProblemError && err.status === 401 ? "Токен не подошёл" : err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card withBorder radius="lg" p="xl" maw={compact ? undefined : 460} mx={compact ? undefined : "auto"} mt={compact ? 0 : "10vh"}>
      <form onSubmit={(e) => void submit(e)}>
        <Stack>
          <Title order={3}>Вход в Manufacture</Title>
          <Text size="sm" c="dimmed">
            Введите admin-токен ядра (<code>MF_ADMIN_TOKEN</code>, его печатает <code>make up</code> при первом старте). Он хранится только в этом браузере.
          </Text>
          <PasswordInput label="Admin-токен" placeholder="mfa_…" value={value} onChange={(e) => setValue(e.currentTarget.value)} autoFocus={!compact} required />
          {error && (
            <Alert color="red" variant="light">
              {error}
            </Alert>
          )}
          <Button type="submit" loading={busy} disabled={!value.trim()}>
            Войти
          </Button>
        </Stack>
      </form>
    </Card>
  );
}
