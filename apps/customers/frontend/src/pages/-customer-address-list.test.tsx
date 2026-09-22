import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CustomerResponse } from "../api/customers";
import type { CustomerRegistryRecord } from "../api/registry";
import { stubFetch } from "../test/fetch";
import { CustomerAddressesSection } from "./-customer-address-list";
import { CustomerBillingCard } from "./-customer-billing-card";

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

const renderSection = (
  fetchMock: ReturnType<typeof vi.fn>,
  { canEdit = true, registryRecord = null }: { canEdit?: boolean; registryRecord?: CustomerRegistryRecord | null } = {},
) => {
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerAddressesSection customerId={1001} canEdit={canEdit} registryRecord={registryRecord} />
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

  it("says the addresses could not be loaded rather than claiming there are none", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 500 }));
    renderSection(fetchMock);

    expect(await screen.findByText("Could not load addresses.")).toBeInTheDocument();
    expect(screen.queryByText("No addresses yet")).not.toBeInTheDocument();
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
    expect(screen.getByRole("button", { name: /^make branch office the primary address$/i })).toBeInTheDocument();
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
    const addressGets = () =>
      fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url) === "/api/v1/customers/1001/addresses" && (init as RequestInit | undefined)?.method === undefined,
      );
    const getsBeforePut = addressGets().length;
    await userEvent.click(screen.getByRole("button", { name: /^make branch office the primary address$/i }));

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
    // The invalidation the title promises: the list is read again, so the
    // badge moves without a page reload.
    await waitFor(() => expect(addressGets().length).toBeGreaterThan(getsBeforePut));
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
    await userEvent.click(screen.getByRole("button", { name: /^delete address — main office$/i }));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/delete this invoice address/i)).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith("/api/v1/customers/1001/addresses/1", { method: "DELETE" });

    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/addresses/1", { method: "DELETE" }),
    );
  });

  it("names each row's actions after its own address, so two addresses of the same type stay individually addressable", async () => {
    const oslo = address({ id: 1, type: "delivery", label: "Warehouse Oslo", line1: "Osloveien 1", isPrimary: true });
    const bergen = address({
      id: 2,
      type: "delivery",
      label: "Warehouse Bergen",
      line1: "Bergensveien 2",
      isPrimary: false,
    });
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/addresses" && (!init || init.method === undefined)) {
        return Promise.resolve(jsonResponse(200, { data: [oslo, bergen] }));
      }
      if (path === "/api/v1/customers/1001/addresses/2" && init?.method === "PUT") {
        return Promise.resolve(jsonResponse(200, { ...bergen, isPrimary: true }));
      }
      if (path === "/api/v1/customers/1001/addresses/1" && init?.method === "DELETE") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderSection(fetchMock);

    await screen.findByText("Warehouse Bergen");

    // Each row's Edit/Delete/Make-primary resolves to exactly one element —
    // the bare "Edit address"/"Delete address" wording would otherwise match
    // two elements per query and throw.
    const editOslo = screen.getByRole("button", { name: /edit address — warehouse oslo/i });
    const editBergen = screen.getByRole("button", { name: /edit address — warehouse bergen/i });
    expect(editOslo).not.toBe(editBergen);
    const deleteOslo = screen.getByRole("button", { name: /delete address — warehouse oslo/i });
    const deleteBergen = screen.getByRole("button", { name: /delete address — warehouse bergen/i });
    expect(deleteOslo).not.toBe(deleteBergen);
    const makeBergenPrimary = screen.getByRole("button", {
      name: /^make warehouse bergen the primary address$/i,
    });

    // Clicking Bergen's "Make primary" acts on Bergen (id 2), never Oslo (id 1).
    await userEvent.click(makeBergenPrimary);
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.filter(
          ([calledUrl, calledInit]) =>
            String(calledUrl) === "/api/v1/customers/1001/addresses/2" && calledInit?.method === "PUT",
        ),
      ).toHaveLength(1),
    );
    expect(
      fetchMock.mock.calls.some(
        ([calledUrl, calledInit]) =>
          String(calledUrl) === "/api/v1/customers/1001/addresses/1" && calledInit?.method === "PUT",
      ),
    ).toBe(false);

    // Clicking Oslo's Delete (not Bergen's) removes Oslo (id 1).
    await userEvent.click(deleteOslo);
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.filter(
          ([calledUrl, calledInit]) =>
            String(calledUrl) === "/api/v1/customers/1001/addresses/1" && calledInit?.method === "DELETE",
        ),
      ).toHaveLength(1),
    );
    expect(
      fetchMock.mock.calls.some(
        ([calledUrl, calledInit]) =>
          String(calledUrl) === "/api/v1/customers/1001/addresses/2" && calledInit?.method === "DELETE",
      ),
    ).toBe(false);
  });
});

/**
 * The registry's two addresses are offered, never imposed (design D3): an
 * address on file may deliberately differ from the register, so the record
 * only puts a prefilled add-address modal one click away, and the ordinary
 * POST does the rest. The record comes down from the Overview tab, which
 * already holds that query.
 */
describe("the registry's addresses, offered beside the customer's own", () => {
  afterEach(() => vi.unstubAllGlobals());

  const registryRecord = (overrides: Partial<CustomerRegistryRecord> = {}): CustomerRegistryRecord => ({
    organisationNumber: "974760673",
    name: "REGISTERENHETEN I BRØNNØYSUND",
    organisationFormCode: null,
    organisationForm: null,
    industryCode: null,
    industry: null,
    employees: null,
    vatRegistered: true,
    bankrupt: false,
    underLiquidation: false,
    underForcedLiquidation: false,
    deletedOn: null,
    foundedOn: null,
    website: null,
    email: null,
    phone: null,
    mobile: null,
    parentOrganisationNumber: null,
    businessAddress: {
      lines: ["Havnegata 48"],
      postalCode: "8900",
      city: "BRØNNØYSUND",
      municipality: "BRØNNØY",
      countryCode: "NO",
    },
    postalAddress: {
      lines: ["Postboks 900", "Sentrum"],
      postalCode: "8910",
      city: "BRØNNØYSUND",
      municipality: "BRØNNØY",
      countryCode: "NO",
    },
    fetchedAt: "2026-09-22T09:00:00Z",
    ...overrides,
  });

  const lastAddressPost = (fetchMock: ReturnType<typeof vi.fn>) =>
    fetchMock.mock.calls
      .filter(
        ([url, init]) =>
          String(url) === "/api/v1/customers/1001/addresses" && (init as RequestInit | undefined)?.method === "POST",
      )
      .at(-1);

  it("offers both of the registry's addresses when the customer has neither", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: [] }));
    renderSection(fetchMock, { registryRecord: registryRecord() });

    expect(await screen.findByRole("button", { name: "Use the registry's business address" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Use the registry's postal address" })).toBeInTheDocument();
  });

  it("offers nothing when there is no record — nothing fetched, or a viewer who may not see it", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: [] }));
    renderSection(fetchMock, { registryRecord: null });

    expect(await screen.findByText("No addresses yet")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Use the registry/ })).not.toBeInTheDocument();
  });

  it("offers only the address the registry actually holds", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: [] }));
    renderSection(fetchMock, { registryRecord: registryRecord({ postalAddress: null }) });

    expect(await screen.findByRole("button", { name: "Use the registry's business address" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Use the registry's postal address" })).not.toBeInTheDocument();
  });

  it("withdraws an offer once the customer has an address of that type", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse(200, { data: [address({ id: 9, type: "visiting", line1: "Osloveien 1" })] }));
    renderSection(fetchMock, { registryRecord: registryRecord() });

    expect(await screen.findByRole("button", { name: "Use the registry's postal address" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Use the registry's business address" })).not.toBeInTheDocument();
  });

  it("offers nothing to a caller who may not add an address", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: [] }));
    renderSection(fetchMock, { canEdit: false, registryRecord: registryRecord() });

    expect(await screen.findByText("No addresses yet")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Use the registry/ })).not.toBeInTheDocument();
  });

  it("opens the business address prefilled as a visiting address, and saves it through the ordinary POST", async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/addresses" && init?.method === "POST") {
        return Promise.resolve(jsonResponse(201, address({ id: 7, type: "visiting", line1: "Havnegata 48" })));
      }
      if (path === "/api/v1/customers/1001/addresses") return Promise.resolve(jsonResponse(200, { data: [] }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderSection(fetchMock, { registryRecord: registryRecord() });

    await userEvent.click(await screen.findByRole("button", { name: "Use the registry's business address" }));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText(/^Address line 1/)).toHaveValue("Havnegata 48");
    expect(within(dialog).getByLabelText("Postal code")).toHaveValue("8900");
    expect(within(dialog).getByLabelText("City")).toHaveValue("BRØNNØYSUND");
    expect(within(dialog).getByRole("combobox", { name: "Address type" })).toHaveValue("Visiting");
    expect(within(dialog).getByRole("combobox", { name: "Country" })).toHaveValue("Norway");

    await userEvent.click(within(dialog).getByRole("button", { name: "Add address" }));

    await waitFor(() => expect(lastAddressPost(fetchMock)).toBeTruthy());
    const [, init] = lastAddressPost(fetchMock) as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      type: "visiting",
      // The registry's own name for the entity is not a label the user chose.
      label: null,
      line1: "Havnegata 48",
      line2: null,
      postalCode: "8900",
      city: "BRØNNØYSUND",
      region: null,
      country: "no",
      isPrimary: true,
    });
  });

  it("keeps every line of the postal address, the first one as line 1 and the rest as line 2", async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/addresses" && init?.method === "POST") {
        return Promise.resolve(jsonResponse(201, address({ id: 8, type: "postal", line1: "Postboks 900" })));
      }
      if (path === "/api/v1/customers/1001/addresses") return Promise.resolve(jsonResponse(200, { data: [] }));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderSection(fetchMock, {
      registryRecord: registryRecord({
        postalAddress: {
          lines: ["Postboks 900", "Sentrum", "Bygg 2"],
          postalCode: "8910",
          city: "BRØNNØYSUND",
          municipality: null,
          countryCode: "NO",
        },
      }),
    });

    await userEvent.click(await screen.findByRole("button", { name: "Use the registry's postal address" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add address" }));

    await waitFor(() => expect(lastAddressPost(fetchMock)).toBeTruthy());
    const [, init] = lastAddressPost(fetchMock) as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toMatchObject({
      type: "postal",
      line1: "Postboks 900",
      line2: "Sentrum, Bygg 2",
    });
  });

  it("falls back to Norway only when the registry named no country at all", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: [] }));
    renderSection(fetchMock, {
      registryRecord: registryRecord({
        businessAddress: {
          lines: ["ul. Budowniczych 12"],
          postalCode: null,
          city: "81-336 GDYNIA",
          municipality: null,
          countryCode: "PL",
        },
        postalAddress: { lines: ["Storgata 1"], postalCode: null, city: null, municipality: null, countryCode: "" },
      }),
    });

    await userEvent.click(await screen.findByRole("button", { name: "Use the registry's business address" }));
    let dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("combobox", { name: "Country" })).toHaveValue("Poland");

    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await userEvent.click(screen.getByRole("button", { name: "Use the registry's postal address" }));
    dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("combobox", { name: "Country" })).toHaveValue("Norway");
  });
});

/**
 * The billing profile's warnings are computed from the customer's addresses
 * at read time (design D4), and the two sub-resources have no other
 * connection: only an address write invalidating the profile's own query
 * keeps `no_invoice_address` honest. Both cards sit on the Overview tab
 * under one query client, which is what this renders.
 */
describe("an address write and the billing card", () => {
  afterEach(() => vi.unstubAllGlobals());

  const customer = {
    id: 1001,
    customerNumber: 5001,
    name: "Acme",
    status: "active",
    type: "business",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    identity: null,
    timelineSummary: { entryCount: 0, latestOccurredOn: null },
    revision: 1,
    contactInfo: { email: null, phone: null, website: null },
  } as CustomerResponse;

  const noInvoiceAddress = "There is no invoice address — add one so invoices have somewhere to be sent.";

  it("drops the no-invoice-address warning as soon as an invoice address is added", async () => {
    let addresses: ReturnType<typeof address>[] = [];
    const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url);
      if (path === "/api/v1/customers/1001/billing-profile") {
        return Promise.resolve(
          jsonResponse(200, { revision: 1, warnings: addresses.length > 0 ? [] : ["no_invoice_address"] }),
        );
      }
      if (path === "/api/v1/customers/1001/addresses" && init?.method === "POST") {
        const created = address({ id: 7, line1: "Storgata 1" });
        addresses = [created];
        return Promise.resolve(jsonResponse(201, created));
      }
      if (path === "/api/v1/customers/1001/addresses") {
        return Promise.resolve(jsonResponse(200, { data: addresses }));
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    stubFetch(fetchMock);

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
      <MantineProvider env="test">
        <Notifications />
        <ModalsProvider>
          <QueryClientProvider client={queryClient}>
            <CustomerBillingCard customerId={1001} customer={customer} canManageBilling />
            <CustomerAddressesSection customerId={1001} canEdit />
          </QueryClientProvider>
        </ModalsProvider>
      </MantineProvider>,
    );

    expect(await screen.findByText(noInvoiceAddress)).toBeInTheDocument();

    await userEvent.click(await screen.findByRole("button", { name: "Add address" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText(/^Address line 1/), "Storgata 1");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add address" }));

    await waitFor(() => expect(screen.queryByText(noInvoiceAddress)).not.toBeInTheDocument());
  });
});
