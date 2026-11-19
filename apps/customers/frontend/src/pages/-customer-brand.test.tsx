import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CustomerRelationshipCard } from "./-customer-relationship-card";

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

import { notifications } from "@mantine/notifications";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

const customerBody = {
  id: 1001,
  customerNumber: 5001,
  name: "Equinor",
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  identity: null,
  revision: 3,
};

// The brand block as the wire sends it: a null `name` is omitted, `own` and
// `group` are null for "decides nothing".
const defaultBrand = {
  own: null,
  group: null,
  effective: { id: "b0", name: "Vantigo Demo AS", source: "default" },
  unavailable: [],
};

const brandOptions = [
  { id: "b0", name: "Vantigo Demo AS", isDefault: true },
  { id: "b1", name: "Nordlys", isDefault: false },
  { id: "b2", name: "Fjordkraft Demo", isDefault: false },
];

const stubFetch = (customer: unknown, putStatus = 200) => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (init?.method === "PUT" && url.endsWith("/brand")) {
      return Promise.resolve(
        putStatus === 200
          ? jsonResponse({
              ...(customer as object),
              revision: 4,
              brand: {
                own: { id: "b1", name: "Nordlys", state: "published" },
                group: null,
                effective: { id: "b1", name: "Nordlys", source: "customer" },
                unavailable: [],
              },
            })
          : jsonResponse(
              { title: "Invalid customer brand", errors: { brandId: ["Brand b9 is not a published brand"] } },
              400,
            ),
      );
    }
    if (url === "/api/v1/customers/brands") return Promise.resolve(jsonResponse(brandOptions));
    if (url === "/api/v1/customers/groups") return Promise.resolve(jsonResponse([]));
    if (url === "/api/v1/customers/tags") return Promise.resolve(jsonResponse([]));
    if (url.startsWith("/api/v1/customers/assignable-users")) return Promise.resolve(jsonResponse([]));
    return Promise.resolve(jsonResponse(customer));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

const renderCard = (canEdit = true) =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <Suspense fallback={null}>
          <CustomerRelationshipCard customerId={1001} canEdit={canEdit} />
        </Suspense>
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("CustomerRelationshipCard: the default brand", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it("names the workspace default when nothing is chosen, and the seller note", async () => {
    stubFetch({ ...customerBody, brand: defaultBrand });
    renderCard();
    expect(await screen.findByText("Vantigo Demo AS, the workspace default")).toBeInTheDocument();
    expect(screen.getByText(/The seller's legal details stay the same/)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Brand for this customer" })).toHaveValue(
        "Use the group's brand, or the workspace default",
      ),
    );
  });

  it("says the brand comes from the group, and from the customer itself", async () => {
    stubFetch({
      ...customerBody,
      group: { id: "g1", name: "Retail" },
      brand: {
        own: null,
        group: { id: "b2", name: "Fjordkraft Demo", state: "published" },
        effective: { id: "b2", name: "Fjordkraft Demo", source: "group" },
        unavailable: [],
      },
    });
    renderCard();
    expect(await screen.findByText("Fjordkraft Demo, from the group Retail")).toBeInTheDocument();
    cleanup();

    stubFetch({
      ...customerBody,
      brand: {
        own: { id: "b1", name: "Nordlys", state: "published" },
        group: null,
        effective: { id: "b1", name: "Nordlys", source: "customer" },
        unavailable: [],
      },
    });
    renderCard();
    expect(await screen.findByText("Nordlys, chosen for this customer")).toBeInTheDocument();
  });

  it("explains a fall-back when the chosen brand is archived, and still shows it in the picker", async () => {
    stubFetch({
      ...customerBody,
      brand: {
        own: { id: "b9", name: "Old brand", state: "archived" },
        group: null,
        effective: { id: "b0", name: "Vantigo Demo AS", source: "default" },
        unavailable: ["customer"],
      },
    });
    renderCard();
    expect(await screen.findByText("Vantigo Demo AS, the workspace default")).toBeInTheDocument();
    expect(
      screen.getByText("The brand chosen for this customer, Old brand, is archived, so the next default applies."),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Brand for this customer" })).toHaveValue("Old brand (archived)"),
    );
  });

  it("offers the published brands and saves the choice with the revision it read", async () => {
    const fetchMock = stubFetch({ ...customerBody, brand: defaultBrand });
    renderCard();
    await userEvent.click(await screen.findByRole("combobox", { name: "Brand for this customer" }));
    await userEvent.click(await screen.findByRole("option", { name: "Nordlys" }));

    await waitFor(() => {
      const puts = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "PUT");
      expect(puts).toHaveLength(1);
      expect(String(puts[0][0])).toBe("/api/v1/customers/1001/brand");
      expect(JSON.parse(String((puts[0][1] as RequestInit).body))).toEqual({ brandId: "b1", revision: 3 });
    });
    expect(await screen.findByText("Nordlys, chosen for this customer")).toBeInTheDocument();
  });

  it("clears the choice with a null", async () => {
    const fetchMock = stubFetch({
      ...customerBody,
      brand: {
        own: { id: "b1", name: "Nordlys", state: "published" },
        group: null,
        effective: { id: "b1", name: "Nordlys", source: "customer" },
        unavailable: [],
      },
    });
    renderCard();
    await userEvent.click(await screen.findByRole("combobox", { name: "Brand for this customer" }));
    await userEvent.click(
      await screen.findByRole("option", { name: "Use the group's brand, or the workspace default" }),
    );

    await waitFor(() => {
      const puts = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "PUT");
      expect(JSON.parse(String((puts[0][1] as RequestInit).body))).toEqual({ brandId: null, revision: 3 });
    });
  });

  it("reports a refused brand in a notification", async () => {
    stubFetch({ ...customerBody, brand: defaultBrand }, 400);
    renderCard();
    await userEvent.click(await screen.findByRole("combobox", { name: "Brand for this customer" }));
    await userEvent.click(await screen.findByRole("option", { name: "Fjordkraft Demo" }));
    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(
        expect.objectContaining({
          color: "red",
          title: "Brand could not be saved",
          message: "Brand b9 is not a published brand",
        }),
      ),
    );
  });

  it("shows the brand read-only to a caller who may not edit", async () => {
    stubFetch({ ...customerBody, brand: defaultBrand });
    renderCard(false);
    expect(await screen.findByText("Vantigo Demo AS, the workspace default")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Brand for this customer" })).not.toBeInTheDocument();
  });
});
