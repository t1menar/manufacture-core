import { create } from "zustand";

import type { Conveyor, Overview, StreamEvent } from "../api/types";
import type { StreamState } from "../api/sse";
import { applyEvent, emptyLive, pruneItems, seedFromConveyor, seedFromOverview, type LiveState } from "./reduce";

type LiveStore = LiveState & {
  connection: StreamState;
  setConnection: (s: StreamState) => void;
  apply: (e: StreamEvent) => void;
  applyBatch: (e: StreamEvent[]) => void;
  seed: (o: Overview) => void;
  seedConveyor: (c: Conveyor) => void;
  prune: () => void;
  clear: () => void;
};

const pick = (s: LiveStore): LiveState => s;

export const useLive = create<LiveStore>()((set) => ({
  ...emptyLive(),
  connection: "closed",
  setConnection: (connection) => set({ connection }),
  apply: (e) => set((s) => applyEvent(pick(s), e, Date.now())),
  applyBatch: (events) =>
    set((s) => {
      const now = Date.now();
      let st: LiveState = pick(s);
      for (const e of events) st = applyEvent(st, e, now);
      return st;
    }),
  seed: (o) => set((s) => seedFromOverview(pick(s), o)),
  seedConveyor: (c) => set((s) => seedFromConveyor(pick(s), c, Date.now())),
  prune: () =>
    set((s) => {
      const next = pruneItems(pick(s), Date.now());
      return next === pick(s) ? s : next;
    }),
  clear: () => set({ ...emptyLive(), connection: "closed" }),
}));
