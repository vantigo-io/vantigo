import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";

vi.mock("../../api/authorization", () => ({ getAuthorizationMe: vi.fn() }));
const getAuthorizationMeMock = vi.mocked(getAuthorizationMe);

import { ProjectPlanningTab } from "./-project-planning-tab";
import "../../i18n";

// The tab lives in the Projects app but calls the planning API, so it is the
// host that decides whether the module is mounted and the caller may read the
// plan. That answer is invisible to the package's own tests, so it is pinned here.
vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    useParams: () => ({ projectId: 31 }),
    // No router in this test: the not-enabled page's "Go home" renders as a plain anchor.
    Link: ({ children }: { children: React.ReactNode }) => <a href="/">{children}</a>,
    useRouter: () => ({ history: { back: vi.fn() } }),
  };
});

vi.mock("@vantigo/planning-ui/components/project-plan", () => ({
  ProjectPlan: ({ projectId }: { projectId: number }) => <div>plan for project {projectId}</div>,
}));

const renderTab = (
  modules?: string[],
  permissions: string[] = ["planning:access", "planning:view"],
  seed: "both" | "session-only" | "none" = "both",
) => {
  if (modules) window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // Seeded, as a click from the tab row leaves them: the gate reads the
  // layout's own session and authorization queries rather than asking again.
  if (seed !== "none") queryClient.setQueryData(sessionQueryKey, { user: { id: "user-1", roles: [] } });
  if (seed === "both") queryClient.setQueryData(["authorization", "me", "none"], { permissions });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ProjectPlanningTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the project page's planning tab", () => {
  afterEach(() => {
    cleanup();
    delete window.__VANTIGO_APP__;
    getAuthorizationMeMock.mockReset();
  });

  it("hands the plan the project the page is on", () => {
    renderTab(["projects", "planning"]);

    expect(screen.getByText("plan for project 31")).toBeInTheDocument();
  });

  // A cold deep link: neither query is in the cache yet.
  it("waits rather than guessing while the caller's permissions are unknown", () => {
    renderTab(["projects", "planning"], [], "none");

    expect(screen.queryByText(/plan for project/)).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Access denied" })).not.toBeInTheDocument();
  });

  it("offers a retry when the caller's permissions could not be read at all", async () => {
    getAuthorizationMeMock.mockRejectedValue(new Error("the network is away"));
    renderTab(["projects", "planning"], [], "session-only");

    expect(await screen.findByRole("heading", { name: "Something went wrong" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Access denied" })).not.toBeInTheDocument();
  });

  // Every Planning call needs planning:access and the plan planning:view,
  // which planning:manage does not imply: a pasted URL without both reaches
  // an API that refuses the read, so it gets the forbidden page instead.
  it.each([
    [["projects:access"]],
    [["projects:access", "planning:access"]],
    [["projects:access", "planning:access", "planning:manage"]],
    [["projects:access", "planning:view"]],
  ])("renders the forbidden page for a deep link with %j", (permissions) => {
    renderTab(["projects", "planning"], permissions);

    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
    expect(screen.queryByText(/plan for project/)).not.toBeInTheDocument();
  });

  it("renders the not-enabled page in place when the installation did not mount planning", () => {
    renderTab(["projects"]);

    expect(screen.getByRole("heading", { name: "Planning is not enabled" })).toBeInTheDocument();
    expect(screen.queryByText(/plan for project/)).not.toBeInTheDocument();
  });
});
