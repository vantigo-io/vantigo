import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { InvoicesList } from "./-invoices-list";
import "../../i18n";

// The list's picker needs customers:view (invoices foundation design D1), which
// the host reads from the caller's effective access; this pins that hand-off.
vi.mock("@vantigo/invoices-ui/pages/invoices", () => ({
  InvoicesPage: ({ canViewCustomers, userDisplayName }: { canViewCustomers: boolean; userDisplayName?: string }) => (
    <div>
      invoices for {userDisplayName} {canViewCustomers ? "with" : "without"} the picker
    </div>
  ),
}));

const { fetchSession, getAuthorizationMe } = vi.hoisted(() => ({ fetchSession: vi.fn(), getAuthorizationMe: vi.fn() }));
vi.mock("../../api/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/auth")>()),
  fetchSession,
}));
vi.mock("../../api/authorization", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/authorization")>()),
  getAuthorizationMe,
}));

const renderList = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <InvoicesList />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the invoice list's entry point", () => {
  it("offers the buyer picker to a customers:view holder, with the caller's name", async () => {
    fetchSession.mockResolvedValue({ user: { id: "u1", displayName: "Ola Nordmann", email: "ola@example.test" } });
    getAuthorizationMe.mockResolvedValue({ permissions: ["invoices:access", "customers:view"] });
    renderList();
    expect(await screen.findByText("invoices for Ola Nordmann with the picker")).toBeInTheDocument();
  });

  it("withholds it from a caller without customers:view", async () => {
    fetchSession.mockResolvedValue({ user: { id: "u1", displayName: "Ola Nordmann", email: "ola@example.test" } });
    getAuthorizationMe.mockResolvedValue({ permissions: ["invoices:access"] });
    renderList();
    expect(await screen.findByText("invoices for Ola Nordmann without the picker")).toBeInTheDocument();
  });
});
