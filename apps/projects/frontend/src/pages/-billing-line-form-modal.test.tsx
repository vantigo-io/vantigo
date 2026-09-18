import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { BillingLine } from "../api/projects";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { BillingLineFormModal, type BillingLineModalState } from "./-billing-line-form-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const products = {
  data: [
    {
      id: 5,
      name: "Project management",
      type: "Service",
      variants: [{ id: 31, sku: "PM-H", unit: "hour" }],
    },
  ],
};

const line: BillingLine = {
  id: 1,
  code: "PM",
  trackableCode: "KVEWEBS-PM",
  variantId: 31,
  variantMissing: false,
  productName: "Project management",
  sku: "PM-H",
  unit: "hour",
  active: true,
  pricing: { mode: "discount", discountPercent: 10 },
  createdAt: "2026-01-01T10:00:00Z",
  updatedAt: "2026-01-01T10:00:00Z",
};

const stubLines = (productsStatus = 200) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/products") {
      return Promise.resolve(
        productsStatus === 200 ? jsonResponse(200, products) : jsonResponse(productsStatus, { title: "Forbidden" }),
      );
    }
    if (url.pathname.startsWith("/api/v1/projects/7/billing-lines") && init?.method) {
      return Promise.resolve(jsonResponse(200, line));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderModal = (state: BillingLineModalState | null, currency: string | undefined = "NOK") => {
  const onClose = vi.fn();
  renderWithProviders(<BillingLineFormModal projectId={7} currency={currency} state={state} onClose={onClose} />);
  return { onClose };
};

/** The amount field, whether Mantine hung the test id on the input or its wrapper. */
const fixedAmountInput = () => {
  const field = screen.getByTestId("fixed-amount");
  return (field.querySelector("input") ?? field) as HTMLElement;
};

const pickVariant = async () => {
  await userEvent.click(screen.getByRole("combobox", { name: "Product variant" }));
  await userEvent.click(await screen.findByRole("option", { name: /Project management/ }));
};

describe("BillingLineFormModal", () => {
  it("hints at the missing products access instead of offering a picker (D15)", async () => {
    stubLines(403);
    renderModal({ mode: "create" });

    expect(await screen.findByText("You need access to products to pick a variant.")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Product variant" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();
  });

  it("creates a line with the code upper-cased and the chosen variant", async () => {
    const fetchMock = stubLines();
    const { onClose } = renderModal({ mode: "create" });

    await pickVariant();
    await userEvent.type(screen.getByLabelText(/line code/i), "pm");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "POST") ?? [];
    expect(String(url)).toBe("/api/v1/projects/7/billing-lines");
    expect(JSON.parse(String(init?.body))).toEqual({ code: "PM", variantId: 31, pricingMode: "list" });
  });

  it("asks for the amount the chosen rule needs, and nothing else", async () => {
    stubLines();
    renderModal({ mode: "create" });

    expect(screen.queryByTestId("fixed-amount")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("radio", { name: "Fixed amount" }));
    expect(screen.getByTestId("fixed-amount")).toBeInTheDocument();
    expect(screen.queryByTestId("discount-percent")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "Discount" }));
    expect(screen.getByTestId("discount-percent")).toBeInTheDocument();
    expect(screen.queryByTestId("fixed-amount")).not.toBeInTheDocument();
  });

  it("refuses a fixed rule without an amount", async () => {
    const fetchMock = stubLines();
    renderModal({ mode: "create" });

    await pickVariant();
    await userEvent.type(screen.getByLabelText(/line code/i), "PM");
    await userEvent.click(screen.getByRole("radio", { name: "Fixed amount" }));
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("A fixed line needs an amount above 0")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  it("refuses a fixed amount of zero", async () => {
    const fetchMock = stubLines();
    renderModal({ mode: "create" });

    await pickVariant();
    await userEvent.type(screen.getByLabelText(/line code/i), "PM");
    await userEvent.click(screen.getByRole("radio", { name: "Fixed amount" }));
    await userEvent.type(fixedAmountInput(), "0");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("A fixed line needs an amount above 0")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  it("cannot price a line at a fixed amount until the project has a currency", async () => {
    stubLines();
    renderWithProviders(<BillingLineFormModal projectId={7} state={{ mode: "create" }} onClose={() => {}} />);

    expect(await screen.findByText("A fixed amount needs the project to have a currency")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Fixed amount" })).toBeDisabled();
    expect(screen.getByRole("radio", { name: "Discount" })).not.toBeDisabled();
  });

  it("edits a line, keeping its rule and offering to switch it off", async () => {
    const fetchMock = stubLines();
    const { onClose } = renderModal({ mode: "edit", line });

    const dialog = await screen.findByRole("dialog", { name: "Edit billing line" });
    expect(within(dialog).getByLabelText(/line code/i)).toHaveValue("PM");
    expect(within(dialog).getByRole("radio", { name: "Discount" })).toBeChecked();
    await userEvent.click(within(dialog).getByRole("switch", { name: "Active" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
    expect(String(url)).toBe("/api/v1/projects/7/billing-lines/1");
    expect(JSON.parse(String(init?.body))).toEqual({
      code: "PM",
      variantId: 31,
      pricingMode: "discount",
      discountPercent: 10,
      active: false,
    });
  });

  it("reports a server field error on the line code", async () => {
    stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/products") return Promise.resolve(jsonResponse(200, products));
      if (init?.method === "POST") {
        return Promise.resolve(jsonResponse(400, { title: "Invalid line", errors: { code: ["PM is already used"] } }));
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    const { onClose } = renderModal({ mode: "create" });

    await pickVariant();
    await userEvent.type(screen.getByLabelText(/line code/i), "PM");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("PM is already used")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });
});
