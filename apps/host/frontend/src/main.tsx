import "@mantine/core/styles.css";
import "@mantine/charts/styles.css";
import "@mantine/notifications/styles.css";
import "@mantine/nprogress/styles.css";
import "@mantine/spotlight/styles.css";
import "@mantine/dates/styles.css";
import "@vantigo/frontend-shell/theme.css";
import "./styles.css";
import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { NavigationProgress } from "@mantine/nprogress";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { appConfig, I18nProvider, initAppConfig, vantigoTheme } from "@vantigo/frontend-shell";
import { StrictMode } from "react";
import ReactDOM from "react-dom/client";
import { setAuthStateClearer, setUnauthorizedHandler } from "./api/request";
import { AccountLanguagePreference } from "./components/account-language-preference";
import { NotFoundPage, RouterError } from "./components/errors";
import { LocaleDatesProvider } from "./components/locale-dates-provider";
import { wireNavigationProgress } from "./lib/navigation-progress";
import { publicPaths } from "./lib/public-paths";
import { routeTree } from "./routeTree.gen";

const queryClient = new QueryClient();
initAppConfig({ title: "Vantigo" });
document.title = appConfig().title;
const router = createRouter({
  routeTree,
  basepath: "/",
  context: { queryClient },
  defaultNotFoundComponent: NotFoundPage,
  defaultErrorComponent: RouterError,
});
setAuthStateClearer(() => queryClient.removeQueries({ queryKey: ["auth", "session"], exact: true }));
setUnauthorizedHandler(() => {
  const pathname = window.location.pathname;
  if (publicPaths.has(pathname)) return;
  const returnTo = `${pathname}${window.location.search}${window.location.hash}`;
  void router.navigate({ to: "/session-expired", search: { returnTo } });
});
wireNavigationProgress(router);
declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

const root = document.getElementById("root");
if (!root) throw new Error("Vantigo app root element is missing");
ReactDOM.createRoot(root).render(
  <StrictMode>
    <MantineProvider theme={vantigoTheme}>
      <NavigationProgress />
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <I18nProvider>
          <LocaleDatesProvider>
            <AccountLanguagePreference />
            <ModalsProvider>
              <RouterProvider router={router} />
            </ModalsProvider>
          </LocaleDatesProvider>
        </I18nProvider>
      </QueryClientProvider>
    </MantineProvider>
  </StrictMode>,
);
