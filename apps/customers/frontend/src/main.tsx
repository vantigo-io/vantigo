import "@mantine/core/styles.css";
import "@mantine/charts/styles.css";
import "@mantine/notifications/styles.css";
import "@mantine/nprogress/styles.css";
import "@mantine/spotlight/styles.css";
import "@mantine/dates/styles.css";

import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { NavigationProgress } from "@mantine/nprogress";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import ReactDOM from "react-dom/client";
import { sessionQueryKey } from "./api/auth";
import { setAuthStateClearer, setUnauthorizedHandler } from "./api/request";
import { wireNavigationProgress } from "./lib/navigation-progress";
import { routeTree } from "./routeTree.gen";

const queryClient = new QueryClient();

const router = createRouter({
  routeTree,
  context: { queryClient },
});

// An API 401 must invalidate the cached identity before the redirect starts.
// Otherwise the root loader can briefly restore the expired user (or loop on a
// stale successful session query) while the router is navigating.
setAuthStateClearer(() => {
  queryClient.removeQueries({ queryKey: sessionQueryKey, exact: true });
});
setUnauthorizedHandler(() => {
  if (window.location.pathname !== "/sign-in") void router.navigate({ to: "/sign-in" });
});

wireNavigationProgress(router);

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

// biome-ignore lint/style/noNonNullAssertion: This will always be in place from vite
const rootElement = document.getElementById("root")!;
if (!rootElement.innerHTML) {
  const root = ReactDOM.createRoot(rootElement);
  root.render(
    <StrictMode>
      <MantineProvider>
        <NavigationProgress />
        <Notifications />
        <QueryClientProvider client={queryClient}>
          <ModalsProvider>
            <RouterProvider router={router} />
          </ModalsProvider>
        </QueryClientProvider>
      </MantineProvider>
    </StrictMode>,
  );
}
