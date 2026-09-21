import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerAddressesSection } from "./-customer-address-list";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const address = (overrides: Record<string, unknown> = {}) => ({
  id: 1,
  type: "invoice",
  label: null,
  line1: "Storgata 1",
  line2: null,
  postalCode: "0150",
  city: "Oslo",
  region: null,
  country: "no",
  isPrimary: true,
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  ...overrides,
});

const renderSection = (fetchMock: ReturnType<typeof vi.fn>, { canEdit = true }: { canEdit?: boolean } = {}) => {
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerAddressesSection customerId={1001} canEdit={canEdit} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
};

describe("CustomerAddressesSection", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows the empty state with an add action when canEdit and there are no addresses", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: [] }));
    renderSection(fetchMock);
    expect(await screen.findByText("No addresses yet")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add address" })).toBeInTheDocument();
  });

  it("hides the add action without canEdit", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: [] }));
    renderSection(fetchMock, { canEdit: false });
    expect(await screen.findByText("No addresses yet")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add address" })).not.toBeInTheDocument();
  });

  it("groups addresses by type in the fixed order invoice, postal, delivery, visiting", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        data: [
          address({ id: 4, type: "visiting", line1: "Visiting st 4", isPrimary: true }),
          address({ id: 1, type: "invoice", line1: "Invoice st 1", isPrimary: true }),
          address({ id: 3, type: "delivery", line1: "Delivery st 3", isPrimary: true }),
          address({ id: 2, type: "postal", line1: "Postal st 2", isPrimary: true }),
        ],
      }),
    );
    renderSection(fetchMock);

    await screen.findByText("Invoice st 1");
    const headings = screen.getAllByText(/^(Invoice|Postal|Delivery|Visiting)$/);
    expect(headings.map((heading) => heading.textContent)).toEqual(["Invoice", "Postal", "Delivery", "Visiting"]);
  });

  it("badges the primary address of a group and offers Make primary on the others", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        data: [
          address({ id: 1, isPrimary: true, line1: "Main office" }),
          address({ id: 2, isPrimary: false, line1: "Branch office" }),
        ],
      }),
    );
    renderSection(fetchMock);

    await screen.findByText("Main office");
    expect(screen.getByText("Primary")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Make primary" })).toBeInTheDocument();
  });

  it("hides Make primary and edit/delete actions without canEdit", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        data: [
          address({ id: 1, isPrimary: true, line1: "Main office" }),
          address({ id: 2, isPrimary: false, line1: "Branch office" }),
        ],
      }),
    );
    renderSection(fetchMock, { canEdit: false });

    await screen.findByText("Main office");
    expect(screen.queryByRole("button", { name: "Make primary" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Edit address")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Delete address")).not.toBeInTheDocument();
  });

  it("Make primary sends the full address back with isPrimary: true and invalidates the addresses query", async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/addresses" && (!init || init.method === undefined)) {
        return Promise.resolve(
          jsonResponse(200, {
            data: [
              address({ id: 1, isPrimary: true, line1: "Main office" }),
              address({ id: 2, isPrimary: false, line1: "Branch office" }),
            ],
          }),
        );
      }
      if (path === "/api/v1/customers/1001/addresses/2" && init?.method === "PUT") {
        return Promise.resolve(jsonResponse(200, address({ id: 2, isPrimary: true, line1: "Branch office" })));
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderSection(fetchMock);

    await screen.findByText("Branch office");
    await userEvent.click(screen.getByRole("button", { name: "Make primary" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/addresses/2", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          type: "invoice",
          label: null,
          line1: "Branch office",
          line2: null,
          postalCode: "0150",
          city: "Oslo",
          region: null,
          country: "no",
          isPrimary: true,
        }),
      }),
    );
  });

  it("Delete confirms before removing an address", async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/addresses" && (!init || init.method === undefined)) {
        return Promise.resolve(jsonResponse(200, { data: [address({ id: 1, line1: "Main office" })] }));
      }
      if (path === "/api/v1/customers/1001/addresses/1" && init?.method === "DELETE") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderSection(fetchMock);

    await screen.findByText("Main office");
    await userEvent.click(screen.getByLabelText("Delete address"));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/delete this invoice address/i)).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/customers/1001/addresses/1", { method: "DELETE" });

    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/addresses/1", { method: "DELETE" }),
    );
  });
});
