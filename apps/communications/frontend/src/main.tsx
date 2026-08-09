import "@mantine/core/styles.css";
import "@mantine/notifications/styles.css";
import "@mantine/tiptap/styles.css";
import "@vantigo/frontend-shell/theme.css";
import "./styles.css";
import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { appConfig, initAppConfig, runtimeBase, vantigoTheme } from "@vantigo/frontend-shell";
import ReactDOM from "react-dom/client";
import { routeTree } from "./routeTree.gen";

const queryClient = new QueryClient();

// Whitelabeling: the backend injects the runtime config (title, logo, support)
// into index.html; the dev server falls back to these defaults.
initAppConfig({ title: "Communications" });
document.title = appConfig().title;
const router = createRouter({ routeTree, basepath: runtimeBase(), context: { queryClient } });
declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("Communications app root element is missing");
ReactDOM.createRoot(rootElement).render(
  <MantineProvider theme={vantigoTheme}>
    <Notifications />
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </MantineProvider>,
);
