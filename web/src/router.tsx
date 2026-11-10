import { Center, Stack, Text, Title } from "@mantine/core";
import { createRootRoute, createRoute, createRouter, Link } from "@tanstack/react-router";

import type { RunStatus } from "./api/types";
import { AppLayout } from "./components/AppLayout";
import { AuditPage } from "./pages/AuditPage";
import { ModuleDetailPage } from "./pages/ModuleDetailPage";
import { ModulesPage } from "./pages/ModulesPage";
import { OverviewPage } from "./pages/OverviewPage";
import { ApplyPage, PipelineDetailPage } from "./pages/PipelineDetailPage";
import { PipelinesPage } from "./pages/PipelinesPage";
import { RunDetailPage } from "./pages/RunDetailPage";
import { RunsPage } from "./pages/RunsPage";
import { SettingsPage } from "./pages/SettingsPage";

function NotFound() {
  return (
    <Center py="xl">
      <Stack align="center">
        <Title order={3}>Страница не найдена</Title>
        <Text component={Link} to="/" c="mf">
          На конвейер
        </Text>
      </Stack>
    </Center>
  );
}

const str = (v: unknown) => (typeof v === "string" && v ? v : undefined);

const rootRoute = createRootRoute({ component: AppLayout, notFoundComponent: NotFound });
const routes = [
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: OverviewPage,
    validateSearch: (s: Record<string, unknown>): { pipeline?: string; view?: string } => ({ pipeline: str(s.pipeline), view: str(s.view) }),
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/pipelines", component: PipelinesPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/pipelines/new", component: ApplyPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/pipelines/$name", component: PipelineDetailPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/modules", component: ModulesPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/modules/$name", component: ModuleDetailPage }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/runs",
    component: RunsPage,
    validateSearch: (s: Record<string, unknown>): { pipeline?: string; status?: RunStatus } => ({ pipeline: str(s.pipeline), status: str(s.status) as RunStatus | undefined }),
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/runs/$id", component: RunDetailPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/audit", component: AuditPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings", component: SettingsPage }),
];

export const routeTree = rootRoute.addChildren(routes);

export function makeRouter(history?: Parameters<typeof createRouter>[0]["history"]) {
  return createRouter({ routeTree, defaultPreload: "intent", ...(history ? { history } : {}) });
}

export const router = makeRouter();

declare module "@tanstack/react-router" {
  // eslint-disable-next-line @typescript-eslint/consistent-type-definitions -- module augmentation needs an interface
  interface Register {
    router: typeof router;
  }
}
