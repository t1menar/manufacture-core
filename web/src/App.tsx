import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { useState } from "react";

import { ProblemError } from "./api/problem";
import { showError } from "./lib/toasts";
import { router as defaultRouter } from "./router";
import { theme } from "./theme/theme";

export function makeQueryClient(): QueryClient {
  return new QueryClient({
    queryCache: new QueryCache({
      onError: (error, query) => {
        if (query.meta?.silent || (error instanceof ProblemError && error.status === 401)) return;
        showError(error);
      },
    }),
    defaultOptions: { queries: { staleTime: 5_000, retry: 1, refetchOnWindowFocus: false } },
  });
}

export function App({ router = defaultRouter, client }: { router?: typeof defaultRouter; client?: QueryClient }) {
  const [qc] = useState(() => client ?? makeQueryClient());
  return (
    <MantineProvider theme={theme} defaultColorScheme="dark">
      <Notifications position="top-right" />
      <QueryClientProvider client={qc}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </MantineProvider>
  );
}
