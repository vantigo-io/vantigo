import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, createRoute, Outlet } from "@tanstack/react-router";
import { isProjectStatus } from "../lib/status";
import { ProjectsPage, type ProjectsSearch } from "../pages/projects.index";
import "../i18n";

const flag = (value: unknown): boolean => value === true || value === "true";

/**
 * Stands in for the host route the package's pages are mounted by (Task 14):
 * the same path and the same `validateSearch` defaults, so a page test drives
 * a real router and reads the URL back instead of mocking navigation.
 */
const validateSearch = (search: Record<string, unknown>): ProjectsSearch => {
  const status = String(search.status ?? "");
  return {
    page: Number(search.page ?? 1),
    search: String(search.search ?? ""),
    status: isProjectStatus(status) ? status : "",
    customerId: search.customerId === undefined ? undefined : Number(search.customerId),
    internal: search.internal === undefined ? undefined : flag(search.internal),
    mine: flag(search.mine),
    ...(flag(search.create) ? { create: true as const } : {}),
  };
};

/**
 * `canCreate` is the host's answer, not the package's, so the stand-in route
 * takes it the way the host route passes it. A fresh tree per call: a route
 * carries its children, and tests mount it more than once.
 */
export const makeRouteTree = (canCreate: boolean) => {
  const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    component: () => <Outlet />,
  });
  return rootRoute.addChildren([
    createRoute({
      getParentRoute: () => rootRoute,
      path: "/projects",
      component: () => <ProjectsPage canCreate={canCreate} />,
      validateSearch,
    }),
  ]);
};
