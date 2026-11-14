import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { TaxCategoryOption, TaxCategoryVatCode } from "../api/tax-categories";
import { jsonResponse, path } from "../test/api";
import { stubFetch } from "../test/fetch";
import { meta, settings, vatCodes } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { SettingsPage } from "./settings";

/** The caller the settings are for: `invoices:manage`. */
const manager = {
  canCreate: true,
  canImportBankFiles: false,
  canIssue: true,
  canManage: true,
  canManageBankFeed: false,
  canQuickInvoice: false,
  canRegisterPayments: false,
  canRunReminders: false,
  canSend: false,
  canSendEhf: false,
  canTakeOnSitePayment: false,
};

const categories: TaxCategoryOption[] = [
  { id: 41, name: "Standard", kind: "standard" },
  { id: 42, name: "Mat", kind: "reduced" },
];

const mappingsPath = "/api/v1/invoices/settings/tax-category-vat-codes";

/**
 * The fetch fake: the mappings are state, a PUT sets one and a DELETE removes
 * it, as the server does (orders design O7); Products answers its categories.
 */
const server = (mappings: TaxCategoryVatCode[]) => {
  let state = mappings;
  return stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta({ capabilities: manager }));
    if (url === "/api/v1/invoices/settings") return jsonResponse(200, settings());
    if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, vatCodes());
    if (url === "/api/v1/products/tax-categories") return jsonResponse(200, categories);
    if (url === mappingsPath) return jsonResponse(200, state);
    const one = new RegExp(`^${mappingsPath}/(\\d+)$`).exec(url);
    if (one && method === "PUT") {
      const mapping = { taxCategoryId: Number(one[1]), vatCodeId: JSON.parse(String(init?.body)).vatCodeId };
      state = [...state.filter((m) => m.taxCategoryId !== mapping.taxCategoryId), mapping];
      return jsonResponse(200, mapping);
    }
    if (one && method === "DELETE") {
      state = state.filter((m) => m.taxCategoryId !== Number(one[1]));
      return new Response(null, { status: 204 });
    }
    return new Response(null, { status: 404 });
  });
};

const calls = (fetchMock: ReturnType<typeof server>, method: string) =>
  fetchMock.actualCalls
    .filter(([, init]) => (init?.method ?? "GET") === method)
    .map(([url, init]) => ({ url: path(url), body: init?.body ? JSON.parse(String(init.body)) : undefined }));

describe("Products' tax categories on the invoice settings", () => {
  it("lists each category with its VAT code, sets one and removes another", async () => {
    const fetchMock = server([{ taxCategoryId: 41, vatCodeId: 1 }]);
    renderWithProviders(<SettingsPage canReadTaxCategories />);

    const card = await screen.findByTestId("tax-categories-card");
    const standard = await within(card).findByRole("combobox", { name: "VAT code for Standard" });
    await waitFor(() => expect(standard).toHaveValue("3 — Utgående mva 25 %"));
    const food = within(card).getByRole("combobox", { name: "VAT code for Mat" });
    expect(food).toHaveValue("");

    // An inactive code is not offered.
    await userEvent.click(food);
    expect(screen.queryByRole("option", { name: /3G — Gammel sats/ })).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("option", { name: "31 — Utgående mva 15 %" }));
    await waitFor(() =>
      expect(calls(fetchMock, "PUT")).toEqual([{ url: `${mappingsPath}/42`, body: { vatCodeId: 2 } }]),
    );
    await waitFor(() => expect(food).toHaveValue("31 — Utgående mva 15 %"));

    // Choosing the selected code again keeps it; only the clear button
    // leaves the category with none.
    await waitFor(() => expect(standard).toBeEnabled());
    await userEvent.click(standard);
    await userEvent.click(await screen.findByRole("option", { name: "3 — Utgående mva 25 %" }));
    expect(standard).toHaveValue("3 — Utgående mva 25 %");
    expect(calls(fetchMock, "DELETE")).toEqual([]);
    await userEvent.click(within(card).getByLabelText("Remove the VAT code of Standard"));
    await waitFor(() => expect(calls(fetchMock, "DELETE")).toEqual([{ url: `${mappingsPath}/41`, body: undefined }]));
    await waitFor(() => expect(standard).toHaveValue(""));
  });

  it("lists a mapped category by its id, and reads nothing from Products, without the permission", async () => {
    const fetchMock = server([{ taxCategoryId: 41, vatCodeId: 1 }]);
    renderWithProviders(<SettingsPage />);

    const card = await screen.findByTestId("tax-categories-card");
    expect(within(card).getByText(/needs the Products module/)).toBeInTheDocument();
    const byId = await within(card).findByRole("combobox", { name: "VAT code for Tax category 41" });
    await waitFor(() => expect(byId).toHaveValue("3 — Utgående mva 25 %"));
    expect(within(card).queryByText("Standard")).not.toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => path(url) === "/api/v1/products/tax-categories")).toBe(false);
  });

  it("keeps a mapping whose category Products no longer has, so it can be removed", async () => {
    server([{ taxCategoryId: 99, vatCodeId: 5 }]);
    renderWithProviders(<SettingsPage canReadTaxCategories />);

    const card = await screen.findByTestId("tax-categories-card");
    expect(
      await within(card).findByRole("combobox", { name: "VAT code for Tax category 99 (no longer in Products)" }),
    ).toBeInTheDocument();
  });
});
