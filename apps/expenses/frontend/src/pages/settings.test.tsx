import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/api";
import { categories, rates, settings } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubExpensesApi } from "../test/server";

const put = (fetchMock: ReturnType<typeof stubExpensesApi>, endsWith: string) => {
  const [url, init] =
    fetchMock.actualCalls.find(
      ([candidate, request]) => (request?.method ?? "GET") !== "GET" && String(candidate).endsWith(endsWith),
    ) ?? [];
  return {
    url: url === undefined ? undefined : String(url),
    body: init?.body ? JSON.parse(String(init.body)) : undefined,
  };
};

describe("SettingsPage", () => {
  it("turns the receipt rule off by leaving the threshold out, which is not the same as zero", async () => {
    const fetchMock = stubExpensesApi({ entries: [], settings: settings({ receiptRequiredOver: 1250 }) });
    renderRoute("/expenses/settings");

    await screen.findByRole("radio", { name: "Over an amount" });
    expect(screen.getByRole("radio", { name: "Over an amount" })).toBeChecked();
    await userEvent.click(screen.getByRole("radio", { name: "Off" }));
    await userEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);

    await waitFor(() => expect(put(fetchMock, "/expenses/settings").url).toBeDefined());
    const body = put(fetchMock, "/expenses/settings").body;
    expect(body).toMatchObject({ defaultCurrency: "NOK", defaultMarkupPercent: 10 });
    expect(body).not.toHaveProperty("receiptRequiredOver");
  });

  it("sends zero for a rule that asks for a receipt every time", async () => {
    const fetchMock = stubExpensesApi({ entries: [], settings: settings({ receiptRequiredOver: 1250 }) });
    renderRoute("/expenses/settings");

    await screen.findByRole("radio", { name: "Always" });
    await userEvent.click(screen.getByRole("radio", { name: "Always" }));
    await userEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);

    await waitFor(() => expect(put(fetchMock, "/expenses/settings").body).toMatchObject({ receiptRequiredOver: 0 }));
  });

  it("asks before closing a period, and says what closing it does", async () => {
    const fetchMock = stubExpensesApi({ entries: [], settings: settings() });
    renderRoute("/expenses/settings");

    const lock = await screen.findByRole("textbox", { name: "Locked before" });
    await userEvent.type(lock, "Apr 1, 2026");
    await userEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);

    const confirm = await screen.findByRole("dialog", { name: "Change the lock?" });
    expect(confirm).toHaveTextContent(/nobody but an expense manager can record/);
    await userEvent.click(within(confirm).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(put(fetchMock, "/expenses/settings").body).toMatchObject({ lockedBefore: "2026-04-01" }),
    );
  });

  it("groups the rates by kind, tells a state rate from an own one, and always lists the mileage kinds", async () => {
    stubExpensesApi({
      entries: [],
      rates: [...rates, { id: 9, kind: "mileage", validFrom: "2026-07-01", value: 5.5, currency: "NOK" }],
    });
    renderRoute("/expenses/settings");

    const table = await screen.findByRole("table", { name: "Mileage" });
    expect(within(table).getAllByText("State rate")).toHaveLength(1);
    expect(within(table).getAllByText("Own rate")).toHaveLength(1);
    // mileage_customer ships with no rows at all and is still listed, so one can be added.
    expect(screen.getByRole("button", { name: "Add a rate to Customer rate per kilometre" })).toBeInTheDocument();
    // A per diem kind with no rows is not listed at all.
    expect(screen.queryByText("Per diem, hotel")).not.toBeInTheDocument();
  });

  it("adds a rate to the kind whose own button was pressed", async () => {
    const fetchMock = stubExpensesApi({ entries: [], rates: [...rates] });
    renderRoute("/expenses/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Add a rate to Passenger supplement" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a rate" });
    expect(within(dialog).getByRole("combobox", { name: "Kind" })).toHaveValue("Passenger supplement");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Value" }), "1.25");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/rates"));
    expect(sent(fetchMock, "POST").body).toMatchObject({ kind: "mileage_passenger", value: 1.25, currency: "NOK" });
  });

  it("says exactly what restoring a kind does before it does it", async () => {
    const fetchMock = stubExpensesApi({ entries: [], rates: [...rates] });
    renderRoute("/expenses/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Restore the state rates of Mileage" }));
    const confirm = await screen.findByRole("dialog", { name: "Restore the state rates?" });
    expect(confirm).toHaveTextContent(/Your own rows, on your own days, are untouched and nothing is removed/);
    await userEvent.click(within(confirm).getByRole("button", { name: "Restore" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/expenses/rates/reset", body: { kind: "mileage" } }),
    );
  });

  it("deactivates a category by name and reorders it with buttons named after it", async () => {
    const fetchMock = stubExpensesApi({ entries: [], categories: [...categories] });
    renderRoute("/expenses/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Deactivate Travel" }));
    await waitFor(() => expect(put(fetchMock, "/expenses/categories/11").body).toMatchObject({ active: false }));

    await userEvent.click(screen.getByRole("button", { name: "Move Meals up" }));
    await waitFor(() => expect(put(fetchMock, "/expenses/categories/12").body).toMatchObject({ position: 1 }));
  });

  it("refuses a category with no name, without asking the server", async () => {
    const fetchMock = stubExpensesApi({ entries: [], categories: [...categories] });
    renderRoute("/expenses/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Add category" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a category" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText("Give the category a name")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => String(url).endsWith("/categories"))).toBe(true);
    expect(
      fetchMock.actualCalls.some(([url, init]) => String(url).endsWith("/categories") && init?.method === "POST"),
    ).toBe(false);
  });
});
