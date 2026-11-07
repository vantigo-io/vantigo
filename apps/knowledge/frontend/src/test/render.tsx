import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import "../i18n";
import { type KnowledgePageAccess, noKnowledgeAccess } from "../lib/access";
import { ARTICLE_ROUTE_PATH, KNOWLEDGE_HOME_ROUTE_PATH, TRASH_ROUTE_PATH } from "../lib/routes";
import { ArticlePage } from "../pages/article";
import { KnowledgeHomePage } from "../pages/home";
import { TrashPage } from "../pages/trash";

/** Stands in for the host's routes: the same paths, each page handed what the host hands it. */
const routeTree = (access: KnowledgePageAccess, extra?: ReactNode) => {
  const root = createRootRoute({ component: () => <Outlet /> });
  const home = createRoute({
    getParentRoute: () => root,
    path: KNOWLEDGE_HOME_ROUTE_PATH,
    validateSearch: (search: Record<string, unknown>): { new?: number } => (search.new === 1 ? { new: 1 } : {}),
    component: () => <KnowledgeHomePage openNew={home.useSearch().new === 1} />,
  });
  const articleRoute = createRoute({
    getParentRoute: () => root,
    path: ARTICLE_ROUTE_PATH,
    component: () => <ArticlePage articleId={articleRoute.useParams().articleId} />,
  });
  const trash = createRoute({
    getParentRoute: () => root,
    path: TRASH_ROUTE_PATH,
    component: () => <TrashPage access={access} />,
  });
  const here = createRoute({ getParentRoute: () => root, path: "/here", component: () => <>{extra}</> });
  return root.addChildren([home, articleRoute, trash, here]);
};

/** Mounts the stand-in routes at a URL; `extra` renders at `/here`. */
export const renderRoute = (url: string, access: KnowledgePageAccess = noKnowledgeAccess, extra?: ReactNode) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree: routeTree(access, extra),
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  const view = render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { router, queryClient, ...view };
};
