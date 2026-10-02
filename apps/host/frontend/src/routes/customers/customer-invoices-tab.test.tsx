import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CustomerInvoicesTab } from "./-customer-invoices-tab";
import "../../i18n";

// The tab is the host's, not the package's: it decides whether the module is
// mounted at all, whether this caller may make a draft for this customer, and
// whose name "Vår ref." starts as. Those answers are invisible to the
// package's own tests, so they are pinned here.
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

vi.mock("@vantigo/invoices-ui", () => ({
  CustomerInvoicesPanel: ({
    customerId,
    canCreate,
    userDisplayName,
  }: {
    customerId: number;
    canCreate: boolean;
    userDisplayName?: string;
  }) => (
    <div>
      customer {customerId} canCreate {String(canCreate)} as {userDisplayName ?? "nobody"}
    </div>
  ),
}));

// The customer as the layout's loader cached it: normalised, so an unmerged
// one carries mergedInto: null, and one never anonymised anonymisation: null.
const customer = (overrides: Record<string, unknown> = {}) => ({
  id: 42,
  customerNumber: 7,
  name: "Acme AS",
  status: "active",
  type: "business",
  mergedInto: null,
  anonymisation: null,
  ...overrides,
});

const renderTab = (permissions: string[], modules?: string[], cached: { data: unknown } = { data: customer() }) => {
  if (modules) window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  vi.mocked(useQuery).mockImplementation((options) =>
    options.queryKey[0] === "authorization"
      ? ({ data: { permissions }, isPending: false, isSuccess: true } as never)
      : options.queryKey[0] === "customers"
        ? ({ ...cached, isPending: cached.data === undefined } as never)
        : ({ data: { user: { id: "user-1", displayName: "Kari Nordmann", roles: [] } } } as never),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <CustomerInvoicesTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

const creator = ["invoices:access", "invoices:create", "customers:view"];

describe("the customer page's invoices tab", () => {
  afterEach(() => {
    cleanup();
    delete window.__VANTIGO_APP__;
  });

  it("offers a new invoice on an active customer with invoices:create and customers:view, as the signed-in user", () => {
    renderTab(creator);
    expect(screen.getByText("customer 42 canCreate true as Kari Nordmann")).toBeInTheDocument();
  });

  it("offers no new invoice without invoices:create or without customers:view", () => {
    renderTab(["invoices:access", "customers:view"]);
    expect(screen.getByText("customer 42 canCreate false as Kari Nordmann")).toBeInTheDocument();

    cleanup();
    renderTab(["invoices:access", "invoices:create"]);
    expect(screen.getByText("customer 42 canCreate false as Kari Nordmann")).toBeInTheDocument();
  });

  it("offers no new invoice on a customer that is not active, merged away or anonymised", () => {
    for (const cached of [
      customer({ status: "archived" }),
      customer({ status: "disabled" }),
      customer({ mergedInto: { id: 2, customerNumber: 2, name: "Acme" } }),
      customer({ anonymisation: { anonymiseOn: "2026-09-12", anonymisedAt: "2026-09-12T02:00:00Z" } }),
    ]) {
      renderTab(creator, undefined, { data: cached });
      expect(screen.getByText("customer 42 canCreate false as Kari Nordmann")).toBeInTheDocument();
      cleanup();
    }
  });

  it("offers no new invoice while the customer is not yet known", () => {
    renderTab(creator, undefined, { data: undefined });
    expect(screen.getByText("customer 42 canCreate false as Kari Nordmann")).toBeInTheDocument();
  });

  it("renders the not-enabled page in place when the installation did not mount invoices", () => {
    renderTab(["*"], ["customers", "projects"]);

    expect(screen.getByRole("heading", { name: "Invoices is not enabled" })).toBeInTheDocument();
    expect(screen.queryByText(/canCreate/)).not.toBeInTheDocument();
  });
});
