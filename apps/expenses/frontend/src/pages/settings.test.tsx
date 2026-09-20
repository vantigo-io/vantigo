import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse, sent } from "../test/api";
import { categories, perDiemRates, rates, settings } from "../test/fixtures";
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
  });

  it("lists every per diem kind and every meal percentage, seeded or not", async () => {
    stubExpensesApi({ entries: [], rates: [...rates, ...perDiemRates] });
    renderRoute("/expenses/settings");

    // The shipped per diem rows, priced in money.
    expect(await screen.findByRole("table", { name: "Per diem, 6 to 12 hours" })).toHaveTextContent("397.00");
    expect(screen.getByRole("table", { name: "Per diem, hotel" })).toHaveTextContent("1,012.00");
    // A percentage is a percentage, not money.
    expect(screen.getByRole("table", { name: "Breakfast deduction" })).toHaveTextContent("20 %");
    expect(screen.getByRole("table", { name: "Dinner deduction" })).toHaveTextContent("50 %");

    // The one kind the state agreement does not price is an empty group with a
    // hint, so an administrator can put their own figure in it.
    expect(screen.queryByRole("table", { name: "Per diem, other lodging" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add a rate to Per diem, other lodging" })).toBeInTheDocument();
    expect(screen.getByText(/the agreement has one overnight rate, and it is the hotel one/)).toBeInTheDocument();
  });

  it("says what a meal percentage with no row in force does to a per diem", async () => {
    stubExpensesApi({ entries: [], rates: [...rates, ...perDiemRates] });
    renderRoute("/expenses/settings");

    const hints = await screen.findAllByText(/a per diem with that meal covered is refused/);
    expect(hints).toHaveLength(3);
  });

  it("saves the business time zone, warning out loud that it moves the days of recorded trips", async () => {
    const fetchMock = stubExpensesApi({ entries: [], settings: settings({ timeZone: "Europe/Oslo" }) });
    renderRoute("/expenses/settings");

    const zone = await screen.findByRole("combobox", { name: "Business time zone" });
    expect(zone).toHaveValue("Europe/Oslo");
    expect(screen.getByText("Changing it moves the days of trips already recorded")).toBeInTheDocument();

    await userEvent.click(zone);
    await userEvent.clear(zone);
    await userEvent.type(zone, "Europe/Stockholm");
    await userEvent.click(await screen.findByRole("option", { name: "Europe/Stockholm" }));
    await userEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);

    // The lock is reversible and is asked about; the zone moves the days of
    // every trip already recorded and nothing puts them back, so it is asked
    // about too.
    const confirm = await screen.findByRole("dialog", { name: "Change the business time zone?" });
    expect(confirm).toHaveTextContent(/Every trip already recorded is re-read in Europe\/Stockholm/);
    await userEvent.click(within(confirm).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(put(fetchMock, "/expenses/settings").body).toMatchObject({ timeZone: "Europe/Stockholm" }),
    );
  });

  it("puts a refused time zone on the field the choice was made in", async () => {
    stubExpensesApi({
      entries: [],
      settings: settings(),
      write: (method, path) =>
        method === "PUT" && path === "/api/v1/expenses/settings"
          ? problemResponse(400, "Invalid settings", { timeZone: ["Mars/Olympus is not a time zone"] })
          : undefined,
    });
    renderRoute("/expenses/settings");

    await screen.findByRole("combobox", { name: "Business time zone" });
    await userEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);

    expect(await screen.findByText("Mars/Olympus is not a time zone")).toBeInTheDocument();
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

  it("writes a percentage with the decimals it was given, not the ones it was rounded to", async () => {
    stubExpensesApi({
      entries: [],
      rates: [{ id: 21, kind: "meal_lunch_percent", validFrom: "2026-01-01", value: 12.5 }],
    });
    renderRoute("/expenses/settings");

    const table = await screen.findByRole("table", { name: "Lunch deduction" });
    expect(table).toHaveTextContent("12.5 %");
  });

  it("follows a settings change made elsewhere while the form is untouched", async () => {
    const server: { settings: ReturnType<typeof settings> } = { settings: settings({ defaultCurrency: "NOK" }) };
    stubExpensesApi({
      entries: [],
      get settings() {
        return server.settings;
      },
    });
    const { queryClient } = renderRoute("/expenses/settings");

    await waitFor(() => expect(screen.getByRole("textbox", { name: "Default currency" })).toHaveValue("NOK"));

    // Somebody else saves a different currency; the open form has to follow it,
    // or the lock confirm below would compare against a value nobody can see.
    server.settings = settings({ defaultCurrency: "EUR" });
    await queryClient.invalidateQueries({ queryKey: ["expenses"] });

    await waitFor(() => expect(screen.getByRole("textbox", { name: "Default currency" })).toHaveValue("EUR"));
  });

  it("leaves a form somebody is typing into alone", async () => {
    const server: { settings: ReturnType<typeof settings> } = { settings: settings({ defaultCurrency: "NOK" }) };
    stubExpensesApi({
      entries: [],
      get settings() {
        return server.settings;
      },
    });
    const { queryClient } = renderRoute("/expenses/settings");

    const currency = await screen.findByRole("textbox", { name: "Default currency" });
    await userEvent.clear(currency);
    await userEvent.type(currency, "SEK");

    server.settings = settings({ defaultCurrency: "EUR" });
    await queryClient.invalidateQueries({ queryKey: ["expenses"] });

    await waitFor(() => expect(screen.getByRole("textbox", { name: "Default currency" })).toHaveValue("SEK"));
  });
});
