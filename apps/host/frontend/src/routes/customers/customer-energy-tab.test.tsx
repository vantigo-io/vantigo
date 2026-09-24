import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CustomerEnergyTab } from "./-customer-energy-tab";
import "../../i18n";

// The tab is the host's, not the package's: it decides whether the module is
// mounted at all and whether this caller may attach a metering point to this
// customer. Those answers are invisible to the package's own tests, so they
// are pinned here, as the Projects tab's are.
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

vi.mock("@vantigo/energy-ui", () => ({
  CustomerEnergyPanel: ({ customerId, canAttach }: { customerId: number; canAttach?: boolean }) => (
    <div>
      customer {customerId} canAttach {String(canAttach)}
    </div>
  ),
}));

// The customer as the layout's loader cached it: normalised, so an unmerged
// one carries mergedInto: null.
const customer = (mergedInto: { id: number; customerNumber: number; name: string } | null = null) => ({
  id: 42,
  customerNumber: 7,
  name: "Acme AS",
  status: mergedInto ? "archived" : "active",
  type: "business",
  mergedInto,
});

const renderTab = (cached: ReturnType<typeof customer>, modules?: string[]) => {
  if (modules) window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  vi.mocked(useQuery).mockImplementation((options) =>
    options.queryKey[0] === "customers" ? ({ data: cached, isPending: false } as never) : ({} as never),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <CustomerEnergyTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the customer page's energy tab", () => {
  afterEach(() => {
    cleanup();
    delete window.__VANTIGO_APP__;
  });

  it("lets the caller attach a metering point, but not to a merged-away customer", () => {
    renderTab(customer());
    expect(screen.getByText("customer 42 canAttach true")).toBeInTheDocument();

    cleanup();
    renderTab(customer({ id: 2, customerNumber: 2, name: "Acme" }));
    expect(screen.getByText("customer 42 canAttach false")).toBeInTheDocument();
  });

  it("renders the not-enabled page in place when the installation did not mount energy", () => {
    renderTab(customer(), ["customers", "projects"]);

    expect(screen.getByRole("heading", { name: "Energy is not enabled" })).toBeInTheDocument();
    expect(screen.queryByText(/canAttach/)).not.toBeInTheDocument();
  });
});
