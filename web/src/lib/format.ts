const TZ = "Europe/Stockholm";

export function formatDateTime(iso: string | undefined | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString("ru-RU", { timeZone: TZ, day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function formatTime(ms: number): string {
  return new Date(ms).toLocaleTimeString("ru-RU", { timeZone: TZ, hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function formatDuration(ms: number | undefined | null): string {
  if (ms === undefined || ms === null || !Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1000) return `${Math.round(ms)} мс`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)} с`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} мин ${Math.round(s % 60)} с`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h} ч ${m % 60} мин`;
  return `${Math.floor(h / 24)} д ${h % 24} ч`;
}

export function between(a?: string, b?: string, now = Date.now()): number | undefined {
  if (!a) return undefined;
  const t0 = Date.parse(a);
  const t1 = b ? Date.parse(b) : now;
  return Number.isNaN(t0) || Number.isNaN(t1) ? undefined : t1 - t0;
}

export function formatBytes(n: number | undefined): string {
  if (n === undefined) return "—";
  const u = ["Б", "КиБ", "МиБ", "ГиБ", "ТиБ"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${u[i]}`;
}

export function formatRate(r: number | undefined): string {
  if (r === undefined) return "—";
  return `${r >= 10 ? r.toFixed(0) : r.toFixed(2)}/с`;
}

export const STATUS_LABELS: Record<string, string> = {
  queued: "в очереди",
  running: "идёт",
  succeeded: "успех",
  failed: "ошибка",
  needs_attention: "нужно внимание",
  cancelled: "отменён",
  pending: "ожидает",
  dispatched: "отправлен",
  skipped: "пропущен",
  ok: "ok",
  error: "ошибка",
  timed_out: "таймаут",
  lost: "потерян",
  up: "работает",
  degraded: "деградация",
  down: "нет реплик",
  unknown: "неизвестно",
  scheduled: "запланирован",
  starting: "запускается",
  ready: "готов",
  unhealthy: "нездоров",
  stopped: "остановлен",
};

export function statusLabel(s: string | undefined): string {
  return (s && STATUS_LABELS[s]) ?? s ?? "—";
}

export function statusColor(s: string | undefined): string {
  switch (s) {
    case "running":
    case "dispatched":
      return "blue";
    case "queued":
    case "pending":
      return "gray";
    case "succeeded":
    case "ok":
    case "up":
    case "ready":
      return "green";
    case "failed":
    case "error":
    case "timed_out":
    case "lost":
    case "down":
      return "red";
    case "needs_attention":
    case "degraded":
      return "orange";
    default:
      return "gray";
  }
}

export function shortId(id: string): string {
  return id.slice(0, 8);
}
