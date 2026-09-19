import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProjectTimeTab } from "./-project-time-tab";
import "../../i18n";

// The tab lives in the Projects app but calls the time API, so it is the host
// that decides whether the module is mounted at all. That answer is invisible
// to the package's own tests, so it is pinned here.
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

vi.mock("@vantigo/time-ui/components/project-time-panel", () => ({
  ProjectTimePanel: ({ projectId }: { projectId: number }) => <div>hours for project {projectId}</div>,
}));

const renderTab = (modules?: string[]) => {
  if (modules) window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ProjectTimeTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the project page's time tab", () => {
  afterEach(() => {
    cleanup();
    delete window.__VANTIGO_APP__;
  });

  it("hands the panel the project the page is on", () => {
    renderTab(["projects", "time"]);

    expect(screen.getByText("hours for project 31")).toBeInTheDocument();
  });

  it("renders the not-enabled page in place when the installation did not mount time", () => {
    renderTab(["projects"]);

    expect(screen.getByRole("heading", { name: "Time is not enabled" })).toBeInTheDocument();
    expect(screen.queryByText(/hours for project/)).not.toBeInTheDocument();
  });
});
