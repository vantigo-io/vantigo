import "@mantine/core/styles.css";
import "@mantine/charts/styles.css";
import "@mantine/notifications/styles.css";
import "@mantine/nprogress/styles.css";
import "@mantine/spotlight/styles.css";
import "@mantine/dates/styles.css";
import "@vantigo/frontend-shell/theme.css";

import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { NavigationProgress } from "@mantine/nprogress";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { appConfig, appUrl, initAppConfig, runtimeBase, vantigoTheme } from "@vantigo/frontend-shell";
import { StrictMode } from "react";
import ReactDOM from "react-dom/client";
import { sessionQueryKey } from "./api/auth";
import { setAuthStateClearer, setUnauthorizedHandler } from "./api/request";
import { wireNavigationProgress } from "./lib/navigation-progress";
import { routeTree } from "./routeTree.gen";

const queryClient = new QueryClient();

// Whitelabeling: the backend injects the runtime config (title, logo, support)
// into index.html; the dev server falls back to these defaults.
initAppConfig({ title: "Customers" });
document.title = appConfig().title;

const router = createRouter({
  routeTree,
  basepath: runtimeBase(),
  context: { queryClient },
});

// An API 401 must invalidate the cached identity before the redirect starts.
// Otherwise the root loader can briefly restore the expired user (or loop on a
// stale successful session query) while the router is navigating.
setAuthStateClearer(() => {
  queryClient.removeQueries({ queryKey: sessionQueryKey, exact: true });
});
setUnauthorizedHandler(() => {
  if (window.location.pathname !== appUrl("/sign-in")) void router.navigate({ to: "/sign-in" });
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
      <MantineProvider theme={vantigoTheme}>
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
