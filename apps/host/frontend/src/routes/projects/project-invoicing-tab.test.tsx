import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sessionQueryKey } from "../../api/auth";
import { ProjectInvoicingTab } from "./-project-invoicing-tab";
import "../../i18n";

// The tab lives on the project page but calls the invoices API, so it is the
// host that decides whether the module is mounted and whether this caller may
// draft an invoice (invoices work design D18): the panel itself is the
// package's, reduced here to the project it was handed.
vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    useParams: () => ({ projectId: 31 }),
    Link: ({ children }: { children: React.ReactNode }) => <a href="/">{children}</a>,
    useRouter: () => ({ history: { back: vi.fn() } }),
  };
});

vi.mock("@vantigo/invoices-ui", () => ({
  UninvoicedWorkPanel: ({ projectId }: { projectId: number }) => <div>uninvoiced work of project {projectId}</div>,
}));

const renderTab = (modules: string[], permissions: string[]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // What a click on the tab row finds in the cache: the layout asked for both.
  queryClient.setQueryData(sessionQueryKey, { user: { id: "user-1", roles: [] } });
  queryClient.setQueryData(["authorization", "me", "none"], { permissions });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ProjectInvoicingTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the project page's Invoicing tab", () => {
  afterEach(() => {
    cleanup();
    delete window.__VANTIGO_APP__;
  });

  it("lists the project's uninvoiced work for a caller with invoices:create", () => {
    renderTab(["projects", "invoices"], ["invoices:access", "invoices:create"]);
    expect(screen.getByText("uninvoiced work of project 31")).toBeInTheDocument();
  });

  it("refuses a caller without invoices:create in place, and asks the invoices API nothing", () => {
    renderTab(["projects", "invoices"], ["invoices:access"]);
    expect(screen.queryByText(/uninvoiced work/)).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
  });

  it("refuses a caller with invoices:create but not invoices:access, which the API asks too", () => {
    renderTab(["projects", "invoices"], ["invoices:create"]);
    expect(screen.queryByText(/uninvoiced work/)).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Access denied" })).toBeInTheDocument();
  });

  it("renders the not-enabled page when the installation did not mount invoices", () => {
    renderTab(["projects"], ["*"]);
    expect(screen.queryByText(/uninvoiced work/)).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Invoices is not enabled" })).toBeInTheDocument();
  });
});
