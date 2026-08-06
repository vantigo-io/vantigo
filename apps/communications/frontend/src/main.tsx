import "@mantine/core/styles.css";
import "@mantine/notifications/styles.css";
import "@mantine/tiptap/styles.css";
import "./styles.css";
import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import ReactDOM from "react-dom/client";
import { routeTree } from "./routeTree.gen";

const queryClient = new QueryClient();
const router = createRouter({ routeTree, context: { queryClient } });
declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("Communications app root element is missing");
ReactDOM.createRoot(rootElement).render(
  <MantineProvider>
    <Notifications />
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </MantineProvider>,
);
