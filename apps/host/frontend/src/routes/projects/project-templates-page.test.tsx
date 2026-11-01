import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProjectTemplatesPage } from "./-project-templates-page";
import "../../i18n";

// Whether this caller may change the template catalog is the host's answer:
// the package takes it as a prop and never reads permissions, the way the
// project list takes `canCreate`. That hand-off is pinned here.
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@vantigo/projects-ui/pages/templates", () => ({
  ProjectTemplatesPage: ({ canManage }: { canManage: boolean }) => <div>canManage {String(canManage)}</div>,
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
        <ProjectTemplatesPage />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the project templates route", () => {
  afterEach(cleanup);

  it("lets the caller manage templates only with projects:create", () => {
    renderPage(["projects:access", "projects:create"]);
    expect(screen.getByText("canManage true")).toBeInTheDocument();

    cleanup();
    renderPage(["projects:access"]);
    expect(screen.getByText("canManage false")).toBeInTheDocument();
  });
});
