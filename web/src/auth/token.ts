import { useSyncExternalStore } from "react";

/** The admin token lives only in this browser (localStorage) and is sent as `Authorization: Bearer`. */
export const TOKEN_KEY = "mf-admin-token";

const listeners = new Set<() => void>();
let memory: string | null = null;

function read(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY);
  } catch {
    return memory;
  }
}

export function getToken(): string | null {
  return read();
}

export function setToken(token: string | null): void {
  const v = token?.trim() ? token.trim() : null;
  memory = v;
  try {
    if (v) localStorage.setItem(TOKEN_KEY, v);
    else localStorage.removeItem(TOKEN_KEY);
  } catch {
    /* private mode: keep it in memory */
  }
  for (const l of listeners) l();
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  const onStorage = (e: StorageEvent) => {
    if (e.key === TOKEN_KEY) cb();
  };
  window.addEventListener("storage", onStorage);
  return () => {
    listeners.delete(cb);
    window.removeEventListener("storage", onStorage);
  };
}

export function useToken(): string | null {
  return useSyncExternalStore(subscribe, read, () => null);
}

/** Masks a token for display: `mfa_ab…yz`. */
export function maskToken(t: string): string {
  if (t.length <= 10) return "•".repeat(t.length);
  return `${t.slice(0, 6)}…${t.slice(-2)}`;
}
