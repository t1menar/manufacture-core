import { getToken, setToken } from "../auth/token";
import { setTransport } from "../api/http";
import { createMockTransport, MOCK_TOKEN } from "./server";
import { Simulator } from "./simulator";

/** Dev/demo mode: swaps the transport for the in-browser simulator. `?stress=1` adds the 20-module pipeline. */
export function installMock(): Simulator {
  const params = new URLSearchParams(window.location.search);
  const sim = new Simulator({
    seed: Number(params.get("seed") ?? 7),
    stress: params.get("stress") !== "0",
    docking: params.get("docking") !== "0",
    speed: Number(params.get("speed") ?? 1),
  });
  // Warm up so the belt is busy on first paint (skipped while modules are still docking).
  if (params.get("docking") === "0") sim.advance(Number(params.get("warmup") ?? 20_000));
  sim.start();
  setTransport(createMockTransport(sim));
  if (!getToken()) setToken(MOCK_TOKEN);
  (window as unknown as { __mfSim?: Simulator }).__mfSim = sim;
  return sim;
}
