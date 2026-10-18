import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications, notifications } from "@mantine/notifications";
import { type QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  type RouteComponent,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";

/**
 * Renders a page component at `path` in a one-route router, with the
 * providers main.tsx mounts (notifications and modals among them), so a page
 * that reads its own route (`Route.useSearch()`, keyed by the route id) or
 * renders a `Link` runs as it does in the app, without the root layout and
 * the guards the whole route tree brings.
 */
export const renderRoute = async (path: string, component: RouteComponent, queryClient: QueryClient) => {
  const root = createRootRoute({ component: Outlet });
  const page = createRoute({ getParentRoute: () => root, path, component });
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  await router.load();
  // The notifications store is the module's, so an earlier test's would queue this one's behind them.
  notifications.clean();
  return render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <ModalsProvider>
          <RouterProvider router={router as never} />
        </ModalsProvider>
      </QueryClientProvider>
    </MantineProvider>,
  );
};
