import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications, notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PackagingResponse, VariantResponse } from "../api/products";
import { stubFetch } from "../test/fetch";
import { PackagingsEditor } from "./-packagings-editor";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const packaging = (id: number, name: string, quantity: number, extra: Partial<PackagingResponse> = {}) =>
  ({ id, name, kind: "case", quantity, sortOrder: 0, createdAt: "", updatedAt: "", ...extra }) as PackagingResponse;

const box = packaging(10, "Box", 15, { barcode: "4006381333931", lengthCm: 30, widthCm: 20, heightCm: 10 });
const pallet = packaging(11, "Euro pallet", 600, { kind: "pallet", containsPackagingId: 10, containsCount: 40 });

const variant = (packagings: PackagingResponse[]): VariantResponse => ({
  id: 5,
  sku: "SCREW-1",
  unit: "pcs",
  optionValues: {},
  effectivePrices: [],
  packagings,
  createdAt: "",
  updatedAt: "",
});

const renderEditor = (packagings: PackagingResponse[]) =>
  render(
    <MantineProvider>
      <ModalsProvider>
        <Notifications />
        <QueryClientProvider
          client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}
        >
          <PackagingsEditor productId={1} variant={variant(packagings)} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );

const conflict = (title: string) => jsonResponse(409, { title, status: 409, detail: "the server's own words" });

const bodyOf = (init?: RequestInit) => JSON.parse(String(init?.body));

describe("PackagingsEditor", () => {
  afterEach(() => {
    cleanup();
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it("shows each packaging, a derived one as the count of the other with its quantity in the unit", () => {
    renderEditor([box, pallet]);

    expect(screen.getByRole("heading", { name: "Packagings" })).toBeInTheDocument();
    const rows = screen.getAllByRole("row");
    expect(within(rows[1]).getByText("15 pcs")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Case")).toBeInTheDocument();
    expect(within(rows[1]).getByText("4006381333931")).toBeInTheDocument();
    expect(within(rows[1]).getByText("30 × 20 × 10 cm")).toBeInTheDocument();
    expect(within(rows[2]).getByText("40 × Box (600 pcs)")).toBeInTheDocument();
    expect(within(rows[2]).getByText("Pallet")).toBeInTheDocument();
  });

  it("creates a packaging holding a quantity", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, box)));
    renderEditor([]);

    expect(screen.getByText(/No packagings yet/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Add packaging" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText(/Name/), { target: { value: " Box " } });
    fireEvent.change(within(dialog).getByLabelText(/Quantity/), { target: { value: "15" } });
    fireEvent.change(within(dialog).getByLabelText("Barcode (GTIN)"), { target: { value: "4006381333931" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(true));
    const [url, init] = fetchMock.actualCalls.find(([, i]) => i?.method === "POST") ?? [];
    expect(String(url)).toBe("/api/v1/products/1/variants/5/packagings");
    expect(bodyOf(init)).toEqual({ name: "Box", kind: "case", quantity: 15, barcode: "4006381333931" });
  });

  it("creates a packaging built from another packaging", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, pallet)));
    renderEditor([box]);

    fireEvent.click(screen.getByRole("button", { name: "Add packaging" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText(/Name/), { target: { value: "Euro pallet" } });
    fireEvent.click(within(dialog).getByText("Other packagings"));
    await userEvent.click(await within(dialog).findByRole("combobox", { name: /^Packaging/ }));
    // The dropdown is portalled beside the modal, which hides everything outside itself.
    await userEvent.click(await screen.findByRole("option", { name: "Box (15 pcs)", hidden: true }));
    fireEvent.change(within(dialog).getByLabelText(/Count/), { target: { value: "40" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(true));
    const [, init] = fetchMock.actualCalls.find(([, i]) => i?.method === "POST") ?? [];
    expect(bodyOf(init)).toEqual({ name: "Euro pallet", kind: "case", containsPackagingId: 10, containsCount: 40 });
  });

  it.each([
    ["Barcode taken", "Another variant or packaging already has this barcode."],
    ["Packaging cycle", "A packaging cannot hold a packaging that is built from it."],
  ])("tells a %s refusal of a save by its title", async (title, message) => {
    stubFetch(() => Promise.resolve(conflict(title)));
    renderEditor([box, pallet]);

    fireEvent.click(screen.getByRole("button", { name: "Edit Box" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.getByText("Could not save the packaging")).toBeInTheDocument();
  });

  it("asks before deleting, and tells a Packaging in use refusal by its title", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(conflict("Packaging in use")));
    renderEditor([box, pallet]);

    fireEvent.click(screen.getByRole("button", { name: "Delete Box" }));
    expect(await screen.findByText("Delete the packaging Box? Stock is not affected.")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "DELETE")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    expect(
      await screen.findByText("Another packaging is built from this one. Change or delete that packaging first."),
    ).toBeInTheDocument();
    const [url] = fetchMock.actualCalls.find(([, init]) => init?.method === "DELETE") ?? [];
    expect(String(url)).toBe("/api/v1/products/1/variants/5/packagings/10");
  });
});
