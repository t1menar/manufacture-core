import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";

import { api } from "../api/client";
import { qk } from "../api/queries";
import { connectStream } from "../api/sse";
import type { StreamEvent } from "../api/types";
import { useToken } from "../auth/token";
import { useLive } from "./store";

const FLUSH_MS = 50;

/**
 * Keeps the live store in sync: seeds it from `/overview`, then applies SSE events in 50 ms batches
 * (one store update per batch even under bursts), refetches on `reset`/`pipeline.applied`,
 * and invalidates REST queries that the events make stale (throttled).
 */
export function useLiveStream(): void {
  const token = useToken();
  const qc = useQueryClient();
  const lastId = useRef<string | undefined>(undefined);

  useEffect(() => {
    if (!token) return;
    const live = useLive.getState();
    let alive = true;
    let queue: StreamEvent[] = [];
    let flushTimer: ReturnType<typeof setTimeout> | undefined;
    let dirtyRuns = false;
    let dirtyModules = false;
    const touchedRuns = new Set<string>();

    const seed = async () => {
      try {
        const o = await api.overview();
        if (!alive) return;
        useLive.getState().seed(o);
        qc.setQueryData(qk.overview, o);
        lastId.current ??= o.last_event_id;
        // Items of the pipelines with runs in flight come from their conveyor snapshot.
        const busy = o.pipelines.filter((p) => p.runs.active + p.runs.queued + p.runs.needs_attention > 0);
        await Promise.all(
          busy.map(async (p) => {
            const c = await api.conveyor(p.name).catch(() => null);
            if (c && alive) useLive.getState().seedConveyor(c);
          }),
        );
      } catch {
        /* the stream state shows the problem */
      }
    };

    const flush = () => {
      flushTimer = undefined;
      const batch = queue;
      queue = [];
      if (!batch.length) return;
      useLive.getState().applyBatch(batch);
      for (const e of batch) {
        if (e.id) lastId.current = e.id;
        if (e.type === "reset" || e.type === "pipeline.applied" || e.type === "pipeline.deleted") {
          void seed();
          void qc.invalidateQueries({ queryKey: qk.pipelines });
        }
        if (e.type.startsWith("run.") || e.type.startsWith("step.")) {
          dirtyRuns = true;
          touchedRuns.add((e.data as { run_id: string }).run_id);
        }
        if (e.type === "module.status" || e.type === "module.deleted") dirtyModules = true;
      }
    };

    const invalidator = setInterval(() => {
      if (dirtyRuns) {
        dirtyRuns = false;
        void qc.invalidateQueries({ queryKey: ["runs"] });
        for (const id of touchedRuns) void qc.invalidateQueries({ queryKey: qk.run(id), exact: true });
        touchedRuns.clear();
      }
      if (dirtyModules) {
        dirtyModules = false;
        void qc.invalidateQueries({ queryKey: qk.modules });
      }
      useLive.getState().prune();
    }, 1500);

    void seed();
    const close = connectStream({
      lastEventId: lastId.current,
      onState: (s) => live.setConnection(s),
      onEvent: (e) => {
        queue.push(e);
        flushTimer ??= setTimeout(flush, FLUSH_MS);
      },
    });
    return () => {
      alive = false;
      close();
      clearTimeout(flushTimer);
      clearInterval(invalidator);
    };
  }, [token, qc]);
}
