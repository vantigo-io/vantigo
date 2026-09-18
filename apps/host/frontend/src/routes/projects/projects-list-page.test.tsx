import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProjectsListPage } from "./-projects-list-page";
import "../../i18n";

// Whether this caller may create a project is the host's answer, not the
// package's: the package takes it as a prop and never reads permissions. That
// hand-off is invisible to the package's own tests, so it is pinned here.
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@vantigo/projects-ui/pages/projects.index", () => ({
  ProjectsPage: ({ canCreate }: { canCreate: boolean }) => <div>canCreate {String(canCreate)}</div>,
}));

const renderPage = (permissions: string[]) => {
  vi.mocked(useQuery).mockImplementation((options) =>
    options.queryKey[0] === "authorization"
      ? ({ data: { permissions }, isPending: false } as never)
      : ({ data: { user: { id: "user-1", roles: [] } } } as never),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ProjectsListPage />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the projects list route", () => {
  afterEach(cleanup);

  it("lets the caller create a project from the list only with projects:create", () => {
    renderPage(["projects:access", "projects:create"]);
    expect(screen.getByText("canCreate true")).toBeInTheDocument();

    cleanup();
    renderPage(["projects:access"]);
    expect(screen.getByText("canCreate false")).toBeInTheDocument();
  });
});
