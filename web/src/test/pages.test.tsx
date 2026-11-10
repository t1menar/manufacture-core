import { createMemoryHistory } from "@tanstack/react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { setTransport } from "../api/http";
import { App, makeQueryClient } from "../App";
import { setToken } from "../auth/token";
import { createMockTransport, MOCK_TOKEN } from "../mock/server";
import { Simulator } from "../mock/simulator";
import { makeRouter } from "../router";

let sim: Simulator;

beforeEach(() => {
  sim = new Simulator({ seed: 3, stress: false, docking: false });
  sim.advance(20_000);
  setTransport(createMockTransport(sim));
});

afterEach(() => {
  sim.stop();
});

function renderAt(path: string) {
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  return render(<App router={router} client={makeQueryClient()} />);
}

describe("pages (mock transport)", () => {
  it("asks for a token, rejects a bad one, accepts the right one", async () => {
    const user = userEvent.setup();
    renderAt("/pipelines");
    const input = await screen.findByLabelText(/Admin-токен/);
    await user.type(input, "bad");
    await user.click(screen.getByRole("button", { name: "Войти" }));
    await screen.findByText("Токен не подошёл");
    const again = screen.getByLabelText(/Admin-токен/);
    await user.clear(again);
    await user.type(again, MOCK_TOKEN);
    await user.click(screen.getByRole("button", { name: "Войти" }));
    const table = await screen.findByTestId("pipelines-table");
    expect(within(table).getByText("geo-analysis")).toBeInTheDocument();
  });

  it("lists modules with health and replicas", async () => {
    setToken(MOCK_TOKEN);
    renderAt("/modules");
    const table = await screen.findByTestId("modules-table");
    expect(within(table).getByText("geo-analyze")).toBeInTheDocument();
    expect(within(table).getAllByRole("row").length).toBeGreaterThan(5);
  });

  it("validates pipeline YAML in the editor", async () => {
    setToken(MOCK_TOKEN);
    renderAt("/pipelines/hello-world");
    const summary = await screen.findByTestId("validation-summary");
    expect(summary).toHaveTextContent(/схема ok/i);
  });

  it("shows a run with its step timeline and context", async () => {
    setToken(MOCK_TOKEN);
    const run = sim.listRuns({ pipeline: "geo-analysis", status: "succeeded" }).items[0];
    expect(run).toBeDefined();
    renderAt(`/runs/${run?.id ?? ""}`);
    await screen.findByTestId("step-timeline");
    expect(await screen.findAllByTestId("blob-ref")).not.toHaveLength(0);
  });

  it("renders the 2D conveyor fallback without WebGL", async () => {
    setToken(MOCK_TOKEN);
    renderAt("/?pipeline=hello-world");
    const svg = await screen.findByTestId("conveyor-2d");
    expect(svg).toHaveAttribute("aria-label", "Конвейер hello-world");
    await waitFor(() => {
      expect(screen.getByTestId("live-indicator")).toHaveAttribute("data-state", "open");
    });
  });
});
