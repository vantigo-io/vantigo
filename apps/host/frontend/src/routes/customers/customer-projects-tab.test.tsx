import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CustomerProjectsTab } from "./-customer-projects-tab";
import "../../i18n";

// The tab is the host's, not the package's: it decides whether the module is
// mounted at all and whether this caller may create a project on this
// customer. Those answers are invisible to the package's own tests, so they
// are pinned here.
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    useParams: () => ({ customerId: 42 }),
    // No router in this test: the not-enabled page's "Go home" renders as a plain anchor.
    Link: ({ children }: { children: React.ReactNode }) => <a href="/">{children}</a>,
    useRouter: () => ({ history: { back: vi.fn() } }),
  };
});

vi.mock("@vantigo/projects-ui", () => ({
  CustomerProjectsPanel: ({ customerId, canCreate }: { customerId: number; canCreate?: boolean }) => (
    <div>
      customer {customerId} canCreate {String(canCreate)}
    </div>
  ),
}));

// The customer as the layout's loader cached it: normalised, so an unmerged
// one carries mergedInto: null, and one never anonymised anonymisation: null.
const customer = (
  mergedInto: { id: number; customerNumber: number; name: string } | null = null,
  anonymisation: { anonymiseOn: string; anonymisedAt: string | null } | null = null,
) => ({
  id: 42,
  customerNumber: 7,
  name: "Acme AS",
  status: mergedInto || anonymisation ? "archived" : "active",
  type: "business",
  mergedInto,
  anonymisation,
});

const renderTab = (permissions: string[], modules?: string[], cached = customer()) => {
  if (modules) window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  vi.mocked(useQuery).mockImplementation((options) =>
    options.queryKey[0] === "authorization"
      ? ({ data: { permissions }, isPending: false } as never)
      : options.queryKey[0] === "customers"
        ? ({ data: cached, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <CustomerProjectsTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the customer page's projects tab", () => {
  afterEach(() => {
    cleanup();
    delete window.__VANTIGO_APP__;
  });

  it("lets the caller create a project from the panel only with projects:create", () => {
    renderTab(["projects:access", "projects:create"]);
    expect(screen.getByText("customer 42 canCreate true")).toBeInTheDocument();

    cleanup();
    renderTab(["projects:access"]);
    expect(screen.getByText("customer 42 canCreate false")).toBeInTheDocument();
  });

  it("offers no create on a merged-away customer, whatever the caller may do", () => {
    renderTab(["projects:access", "projects:create"], undefined, customer({ id: 2, customerNumber: 2, name: "Acme" }));
    expect(screen.getByText("customer 42 canCreate false")).toBeInTheDocument();
  });

  it("offers no create on an anonymised customer, while one only scheduled still offers it", () => {
    renderTab(
      ["projects:access", "projects:create"],
      undefined,
      customer(null, { anonymiseOn: "2099-01-31", anonymisedAt: null }),
    );
    expect(screen.getByText("customer 42 canCreate true")).toBeInTheDocument();

    cleanup();
    renderTab(
      ["projects:access", "projects:create"],
      undefined,
      customer(null, { anonymiseOn: "2026-09-12", anonymisedAt: "2026-09-12T02:00:00Z" }),
    );
    expect(screen.getByText("customer 42 canCreate false")).toBeInTheDocument();
  });

  it("renders the not-enabled page in place when the installation did not mount projects", () => {
    renderTab(["*"], ["customers", "energy"]);

    expect(screen.getByRole("heading", { name: "Projects is not enabled" })).toBeInTheDocument();
    expect(screen.queryByText(/canCreate/)).not.toBeInTheDocument();
  });
});
