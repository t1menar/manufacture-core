import "@mantine/core/styles.css";
import "@mantine/notifications/styles.css";
import "@mantine/charts/styles.css";
import "./styles.css";

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./App";

async function boot() {
  if (__MF_MOCK__) {
    const { installMock } = await import("./mock/install");
    installMock();
  }
  const el = document.getElementById("root");
  if (!el) throw new Error("root element missing");
  createRoot(el).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
}

void boot();
