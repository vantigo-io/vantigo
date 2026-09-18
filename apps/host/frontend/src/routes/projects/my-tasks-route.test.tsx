import { QueryClient } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { taskUrl } from "@vantigo/projects-ui/lib/tasks";
import { describe, expect, it } from "vitest";
import { routeTree } from "../../routeTree.gen";

/**
 * The route ids the generated tree matches for a path, driven through the real
 * router rather than read off the file names. `/projects/my-tasks` is a static
 * segment sitting beside `/projects/$projectId`, whose params parser turns the
 * segment into a number — so if the dynamic route ever won the match, My tasks
 * would ask the API for project NaN instead of rendering. TanStack ranks static
 * segments above dynamic ones; this pins that the tree we ship really does.
 */
const matchedRouteIds = (pathname: string) => {
  const router = createRouter({
    routeTree,
    context: { queryClient: new QueryClient() },
    history: createMemoryHistory({ initialEntries: [pathname] }),
  });
  return router.matchRoutes(pathname, {}).map((match) => match.routeId);
};

describe("the projects route tree", () => {
  it("matches /projects/my-tasks on the static route, not the project detail one", () => {
    const ids = matchedRouteIds("/projects/my-tasks");

    expect(ids).toContain("/projects/my-tasks");
    expect(ids).not.toContain("/projects/$projectId");
  });

  it("still matches a project id, and its tasks tab, on the dynamic route", () => {
    expect(matchedRouteIds("/projects/31")).toContain("/projects/$projectId/");
    expect(matchedRouteIds("/projects/31/tasks")).toContain("/projects/$projectId/tasks");
  });
});

/**
 * My tasks links a task through the package's `taskUrl`, which spells the whole
 * URL — query string included — into the single `to` string `ShellLinkProvider`
 * passes on. The host hands TanStack's own `Link` over as that component, so
 * this pins that `Link` splits the search off rather than escaping the `?` into
 * the pathname, which would 404 every task link on the page.
 */
describe("the task deep link the host's link component has to carry", () => {
  const renderLink = async (to: string) => {
    const root = createRootRoute({ component: () => <Outlet /> });
    const projects = createRoute({ getParentRoute: () => root, path: "/projects" });
    const tasks = createRoute({
      getParentRoute: () => projects,
      path: "/$projectId/tasks",
      // The real validator is pinned in route-guards.test.ts; what this
      // miniature tree is for is the URL the link hands the router.
      validateSearch: (search: Record<string, unknown>) => ({ task: Number(search.task) || undefined }),
      component: () => <div>tasks tab</div>,
    });
    const myTasks = createRoute({
      getParentRoute: () => projects,
      path: "/my-tasks",
      component: () => <Link to={to as never}>open the task</Link>,
    });
    const router = createRouter({
      routeTree: root.addChildren([projects.addChildren([tasks, myTasks])]),
      history: createMemoryHistory({ initialEntries: ["/projects/my-tasks"] }),
    });
    render(<RouterProvider router={router as never} />);
    return { router, link: await screen.findByRole("link", { name: "open the task" }) };
  };

  it("keeps the query string out of the pathname and lands on the tab with the task", async () => {
    const { router, link } = await renderLink(taskUrl(31, 1042));

    expect(link).toHaveAttribute("href", "/projects/31/tasks?task=1042");

    await userEvent.click(link);

    expect(await screen.findByText("tasks tab")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/projects/31/tasks");
    expect(router.state.location.search).toEqual({ task: 1042 });
  });
});
