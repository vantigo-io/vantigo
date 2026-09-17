import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { ProductFormModal } from "./-product-form-modal";

const product = {
  id: 1,
  name: "Widget",
  sku: "W-1",
  type: "Goods" as const,
  status: "Active" as const,
  unit: "pcs",
  standardCost: 1,
  taxCategory: { id: 1, name: "Standard", kind: "Standard", rate: 0.25 },
  category: null,
  effectivePrices: [],
  variants: [
    {
      id: 1,
      sku: "W-1",
      unit: "pcs",
      standardCost: 1,
      optionValues: {},
      effectivePrices: [],
      createdAt: "",
      updatedAt: "",
    },
  ],
  createdAt: "",
  updatedAt: "",
};

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const stubLookups = () =>
  stubFetch((url: RequestInfo | URL, init?: RequestInit) => {
    if (String(url) === "/api/v1/products/tax-categories")
      return Promise.resolve(jsonResponse(200, [{ id: 1, name: "Standard", kind: "Standard", rate: 0.25 }]));
    if (String(url) === "/api/v1/products/categories") return Promise.resolve(jsonResponse(200, []));
    if (String(url) === "/api/v1/products" && init?.method === "POST")
      return Promise.resolve(jsonResponse(201, { ...product, name: "Consulting", type: "Service" }));
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderModal = (state: Parameters<typeof ProductFormModal>[0]["state"], onClose = vi.fn()) =>
  render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}
      >
        <ProductFormModal state={state} onClose={onClose} />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("ProductFormModal", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("renders the new DTO tax category picker", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    renderModal({ mode: "edit", product });
    expect(screen.getByRole("combobox", { name: "Tax category" })).toBeInTheDocument();
  });

  it("asks what kind of product it is before showing the form", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    renderModal({ mode: "create" });

    expect(screen.getByRole("button", { name: /Goods/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Service/ })).toBeInTheDocument();
    expect(screen.getByText("A physical item you stock, ship or hand over.")).toBeInTheDocument();
    expect(screen.getByText("Work or access you deliver, with nothing to ship.")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Name" })).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Type" })).not.toBeInTheDocument();
  });

  it("shows the form for the chosen kind and submits it as the product type", async () => {
    const fetchMock = stubLookups();
    renderModal({ mode: "create" });

    await userEvent.click(screen.getByRole("button", { name: /Service/ }));

    expect(screen.getByRole("textbox", { name: "Name" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Category" })).toBeInTheDocument();
    expect(screen.getByText(/default variant/i)).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Type" })).not.toBeInTheDocument();
    expect(screen.getByText("Service")).toBeInTheDocument();

    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "Consulting");
    await userEvent.click(await screen.findByRole("combobox", { name: "Tax category" }));
    await userEvent.click(await screen.findByRole("option", { name: "Standard (25.00%)" }));
    await userEvent.click(screen.getByRole("button", { name: "Create product" }));

    await waitFor(() => {
      const post = fetchMock.actualCalls.find(([, init]) => init?.method === "POST");
      expect(post).toBeDefined();
      expect(JSON.parse(String(post?.[1]?.body))).toMatchObject({ name: "Consulting", type: "Service" });
    });
  });

  it("lets the user go back and pick the other kind", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    renderModal({ mode: "create" });

    await userEvent.click(screen.getByRole("button", { name: /Goods/ }));
    expect(screen.getByRole("textbox", { name: "Name" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Change" }));
    expect(screen.queryByRole("textbox", { name: "Name" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Service/ })).toBeInTheDocument();
  });

  it("starts over at the kind picker when the modal is reopened", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    const { rerender } = renderModal({ mode: "create" });
    await userEvent.click(screen.getByRole("button", { name: /Goods/ }));
    expect(screen.getByRole("textbox", { name: "Name" })).toBeInTheDocument();

    rerender(
      <MantineProvider env="test">
        <Notifications />
        <QueryClientProvider client={new QueryClient()}>
          <ProductFormModal state={null} onClose={vi.fn()} />
        </QueryClientProvider>
      </MantineProvider>,
    );
    rerender(
      <MantineProvider env="test">
        <Notifications />
        <QueryClientProvider client={new QueryClient()}>
          <ProductFormModal state={{ mode: "create" }} onClose={vi.fn()} />
        </QueryClientProvider>
      </MantineProvider>,
    );

    expect(await screen.findByRole("button", { name: /Goods/ })).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Name" })).not.toBeInTheDocument();
  });

  it("edits an existing product in one step with the type select", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    renderModal({ mode: "edit", product });

    expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("Widget");
    expect(screen.getByRole("combobox", { name: "Type" })).toBeInTheDocument();
    expect(screen.queryByText("A physical item you stock, ship or hand over.")).not.toBeInTheDocument();
  });
});
