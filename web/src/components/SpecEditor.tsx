import { Alert, Badge, Button, Group, Modal, Stack, Text } from "@mantine/core";
import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { api } from "../api/client";
import { ProblemError } from "../api/problem";
import type { ApplyResult } from "../api/types";
import { showError, showOk } from "../lib/toasts";
import { validateSpecText } from "../lib/validate";
import { useLive } from "../live/store";
import { DiffView } from "./DiffView";
import { YamlEditor, type ExternalDiagnostic } from "./YamlEditor";

/**
 * Edit → validate (in the browser, then `dry_run` on the core) → review the diff → apply.
 * All paths end in `POST /api/v1/apply`, the same operation as `mfctl apply`.
 */
export function SpecEditor({ initial, baseline, readOnly = false, onApplied }: { initial: string; baseline: string; readOnly?: boolean; onApplied?: (r: ApplyResult) => void }) {
  const [text, setText] = useState(initial);
  const [server, setServer] = useState<ExternalDiagnostic[]>([]);
  const [dry, setDry] = useState<ApplyResult | null>(null);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const modules = useLive((s) => s.modules);
  const qc = useQueryClient();
  const { diagnostics } = useMemo(() => validateSpecText(text, Object.keys(modules)), [text, modules]);
  const errors = diagnostics.filter((d) => d.severity === "error");
  const warnings = diagnostics.filter((d) => d.severity === "warning");

  const handle = (e: unknown) => {
    if (e instanceof ProblemError && e.problem.errors?.length) {
      setServer(e.problem.errors.map((x) => ({ line: x.line ?? 1, message: `${x.path ? `${x.path}: ` : ""}${x.message}`, severity: "error" as const })));
      showError(e, "Ядро отклонило спек");
    } else showError(e);
  };

  const check = async () => {
    setBusy(true);
    setServer([]);
    try {
      const r = await api.apply(text, true);
      setDry(r);
      setConfirm(true);
    } catch (e) {
      handle(e);
    } finally {
      setBusy(false);
    }
  };

  const apply = async () => {
    setBusy(true);
    try {
      const r = await api.apply(text, false);
      setConfirm(false);
      setDry(null);
      showOk(r.results.map((x) => `${x.kind}/${x.name}: ${x.action} r${x.revision}`).join(", ") || "нечего применять", "Применено");
      void qc.invalidateQueries({ queryKey: ["pipelines"] });
      void qc.invalidateQueries({ queryKey: ["modules"] });
      onApplied?.(r);
    } catch (e) {
      setConfirm(false);
      handle(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Stack>
      {readOnly && (
        <Alert color="gray" variant="light">
          Ресурс управляется из git (<code>manufacture.dev/managed-by: git</code>): в панели только чтение.
        </Alert>
      )}
      <YamlEditor value={text} onChange={readOnly ? undefined : (v) => {
        setText(v);
        setServer([]);
      }} diagnostics={diagnostics} external={server} readOnly={readOnly} />
      <Group justify="space-between">
        <Group gap="xs" data-testid="validation-summary">
          {errors.length === 0 ? <Badge color="green" variant="light">схема ok</Badge> : <Badge color="red">{errors.length} ошибок</Badge>}
          {warnings.length > 0 && <Badge color="yellow" variant="light">{warnings.length} предупреждений</Badge>}
          {errors[0] && (
            <Text size="xs" c="red.4">
              строка {errors[0].line}: {errors[0].path ? `${errors[0].path}: ` : ""}
              {errors[0].message}
            </Text>
          )}
        </Group>
        {!readOnly && (
          <Group gap="xs">
            <Button variant="default" onClick={() => setText(initial)} disabled={text === initial}>
              Сбросить
            </Button>
            <Button onClick={() => void check()} loading={busy} disabled={errors.length > 0 || !text.trim()}>
              Проверить и применить…
            </Button>
          </Group>
        )}
      </Group>
      <Modal opened={confirm} onClose={() => setConfirm(false)} title="Применить изменения" size="xl">
        <Stack>
          <Group gap="xs">
            {dry?.results.map((r) => (
              <Badge key={`${r.kind}/${r.name}`} color={r.action === "unchanged" ? "gray" : r.action === "created" ? "green" : "blue"} variant="light">
                {r.kind}/{r.name}: {r.action}
                {r.action !== "unchanged" ? ` → r${r.revision}` : ""}
              </Badge>
            ))}
          </Group>
          {dry?.warnings?.map((w, i) => (
            <Text key={i} size="xs" c="yellow">
              {w.line ? `строка ${w.line}: ` : ""}
              {w.message}
            </Text>
          ))}
          <DiffView before={baseline} after={text} />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setConfirm(false)}>
              Отмена
            </Button>
            <Button onClick={() => void apply()} loading={busy} disabled={dry?.results.every((r) => r.action === "unchanged")}>
              Применить
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
}
