import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { CollectionRateList } from "../api/rates";
import type { ReminderSettings } from "../api/reminder-settings";
import { jsonResponse, refusal } from "../test/api";
import { requestTo } from "../test/document-server";
import { stubFetch } from "../test/fetch";
import {
  CURRENT_USER_ID,
  collectionRates,
  meta,
  OTHER_USER_ID,
  reminderSettings,
  settings,
  vatCodes,
} from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { SettingsPage } from "./settings";

/** A manager: meta's `canManage` is what shows the page (D12). */
const manager = { ...meta().capabilities, canManage: true };

interface World {
  reminders: ReminderSettings;
  rates: CollectionRateList;
}

/**
 * The fetch fake for the settings page's reminder cards: the reminder
 * settings are state, saved only at their current revision — a stale one is a
 * 409 without a code — and the rates are read from `world`; `answers` answers
 * a "METHOD url" otherwise.
 */
const server = (
  answers: Record<string, () => Response> = {},
  world: World = { reminders: reminderSettings(), rates: collectionRates() },
) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    const answer = answers[`${method} ${url}`];
    if (answer) return answer();
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta({ capabilities: manager }));
    if (url === "/api/v1/invoices/settings") return jsonResponse(200, settings());
    if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, vatCodes());
    if (url === "/api/v1/invoices/collection-rates" && method === "GET") return jsonResponse(200, world.rates);
    if (url === "/api/v1/invoices/settings/reminders" && method === "PUT") {
      const body = JSON.parse(String(init?.body)) as ReminderSettings;
      if (body.revision !== world.reminders.revision) {
        return jsonResponse(409, { title: "Reminder settings revision conflict", status: 409, detail: "English." });
      }
      world.reminders = { ...world.reminders, ...body, revision: body.revision + 1 };
      return jsonResponse(200, world.reminders);
    }
    if (url === "/api/v1/invoices/settings/reminders") return jsonResponse(200, world.reminders);
    return new Response(null, { status: 404 });
  });

const renderPage = () => renderWithProviders(<SettingsPage currentUserId={CURRENT_USER_ID} />);

describe("SettingsReminders_EveryFieldAndTheReview", () => {
  it("shows every field with its bounds in words, the regime explained, and who reviewed it", async () => {
    server();
    renderPage();

    const card = await screen.findByTestId("reminder-settings");
    await within(card).findByRole("checkbox", { name: /Offer reminders/ });
    for (const [label, value, bounds] of [
      ["First reminder, days after the due date", "14", "1 to 60 days, counted from the due date"],
      ["Each letter's deadline, in days", "14", "14 to 60 days after the letter is sent — the law asks at least 14"],
      ["Grace days after a deadline", "3", "1 to 10 days before the next letter"],
      ["Reminders before the debt collection notice", "1", "0 to 2."],
      ["Bank data is old after, in days", "3", "1 to 30 days."],
    ]) {
      const input = within(card).getByRole("textbox", { name: label });
      expect(input).toHaveValue(value);
      expect(input).toHaveAccessibleDescription(expect.stringContaining(bounds));
    }
    expect(within(card).getByRole("checkbox", { name: /Send a debt collection notice/ })).toBeChecked();
    expect(within(card).getByRole("checkbox", { name: /Claim late interest/ })).not.toBeChecked();
    expect(within(card).getByRole("combobox", { name: "Charge to a person" })).toHaveValue("The reminder fee");
    expect(within(card).getByRole("combobox", { name: "Charge to a business" })).toHaveValue("The reminder fee");
    expect(within(card).getByText(/The new debt collection act \(LOV-2026-05-22-19\)/)).toBeInTheDocument();
    expect(within(card).getByRole("textbox", { name: "The new act applies from" })).toHaveValue("");
    const review = within(card).getByRole("textbox", { name: "Regime reviewed through" });
    expect(review).toHaveValue("Dec 31, 2026");
    expect(review).toHaveAccessibleDescription(expect.stringContaining("At most a year ahead: Sep 12, 2027."));
    expect(within(card).getByTestId("regime-reviewed")).toHaveTextContent(/^Set by a release /);
  });

  it("says who last reviewed the regime", async () => {
    server({}, { reminders: reminderSettings({ regimeReviewedBy: OTHER_USER_ID }), rates: collectionRates() });
    renderPage();

    expect(await screen.findByTestId("regime-reviewed")).toHaveTextContent(/^Last reviewed .* by another user\.$/);
  });

  it("saves every field with the revision it read", async () => {
    const fetchMock = server();
    renderPage();

    const card = await screen.findByTestId("reminder-settings");
    await userEvent.click(await within(card).findByRole("checkbox", { name: /Offer reminders/ }));
    const grace = within(card).getByRole("textbox", { name: "Grace days after a deadline" });
    await userEvent.clear(grace);
    await userEvent.type(grace, "5");
    await userEvent.click(within(card).getByRole("combobox", { name: "Charge to a business" }));
    await userEvent.click(await screen.findByRole("option", { name: "The business compensation" }));
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(requestTo(fetchMock, "PUT", "/api/v1/invoices/settings/reminders")).toEqual({
        enabled: true,
        firstReminderDays: 14,
        deadlineDays: 14,
        graceDays: 5,
        remindersBeforeNotice: 1,
        collectionNotice: true,
        personCharge: "fee",
        businessCharge: "compensation",
        lateInterest: false,
        staleImportDays: 3,
        inkassolov2026From: null,
        regimeReviewedThrough: "2026-12-31",
        revision: 1,
      }),
    );
    expect(await screen.findByText("Saved")).toBeInTheDocument();
  });

  it("says the settings changed on a stale save, and reloads the latest on request", async () => {
    const world = { reminders: reminderSettings(), rates: collectionRates() };
    server({}, world);
    renderPage();

    const card = await screen.findByTestId("reminder-settings");
    await userEvent.click(await within(card).findByRole("checkbox", { name: /Claim late interest/ }));
    // Someone else saves meanwhile.
    world.reminders = reminderSettings({ revision: 2, graceDays: 7 });
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));
    expect(await within(card).findByText("The reminder settings changed")).toBeInTheDocument();
    expect(screen.queryByText("English.")).not.toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Reload" }));
    await waitFor(() =>
      expect(within(card).getByRole("textbox", { name: "Grace days after a deadline" })).toHaveValue("7"),
    );
    expect(within(card).queryByText("The reminder settings changed")).not.toBeInTheDocument();
  });

  it("puts a refused field's bounds on its own input, in words", async () => {
    server({
      "PUT /api/v1/invoices/settings/reminders": () =>
        jsonResponse(400, {
          title: "Invalid",
          status: 400,
          errors: {
            graceDays: ["graceDays is between 1 and 10"],
            regimeReviewedThrough: ["The regime is reviewed at most a year ahead, through 2027-09-12"],
          },
        }),
    });
    renderPage();

    const card = await screen.findByTestId("reminder-settings");
    await userEvent.click(await within(card).findByRole("checkbox", { name: /Claim late interest/ }));
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));
    expect(await within(card).findByText("A whole number of days, 1 to 10.")).toBeInTheDocument();
    expect(within(card).getByText("A day at most a year after today.")).toBeInTheDocument();
    expect(screen.queryByText("graceDays is between 1 and 10")).not.toBeInTheDocument();
  });
});

describe("SettingsRates_AddDeleteAndWarn", () => {
  it("lists the rows by kind, highlights the one in force, and warns where a release differs", async () => {
    server();
    renderPage();

    const card = await screen.findByTestId("collection-rates");
    expect(await within(card).findByTestId("rates-differ")).toHaveTextContent(
      "A release brought another value for a rate a manager added.",
    );
    const interest = within(card).getByRole("table", { name: "Late interest rate" });
    const rows = within(interest).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.getAttribute("data-rate"))).toEqual(["1005", "1006", "1015"]);
    expect(rows[1]).toHaveAttribute("data-in-force", "true");
    expect(within(rows[1]).getByText("In force")).toBeInTheDocument();
    expect(rows[0]).not.toHaveAttribute("data-in-force");
    expect(within(rows[1]).getByText("12.25%")).toBeInTheDocument();
    expect(within(rows[1]).getByText("A release")).toBeInTheDocument();
    expect(within(rows[2]).getByText("you")).toBeInTheDocument();
    expect(within(rows[2]).getByTestId("release-differs-1015")).toHaveTextContent(
      "The release says 12.75% (FOR-2026-12-18-2222)",
    );
    const inkassosats = within(card).getByRole("table", { name: "Inkassosats" });
    expect(within(inkassosats).getByText(/NOK\s?750\.00/)).toBeInTheDocument();
  });

  it("offers deletion of a manager's future unused row only, never a seeded one, not even a future one", async () => {
    const rates = collectionRates();
    // A release seeds the next half-year's rate ahead of it: future, unused, and still never deleted.
    rates.rates.push({
      ...rates.rates[2],
      id: 1020,
      kind: "b2b_compensation_nok",
      validFrom: "2027-01-01",
      value: 430,
      sourceRef: "FOR-2026-12-18-3333",
      seeded: true,
      inForce: false,
      usable: true,
      createdBy: undefined,
    });
    server({}, { reminders: reminderSettings(), rates });
    renderPage();

    const card = await screen.findByTestId("collection-rates");
    expect(
      await within(card).findByRole("button", { name: "Delete Late interest rate from Jan 1, 2027" }),
    ).toBeInTheDocument();
    expect(within(card).getByRole("table", { name: "Business compensation" })).toHaveTextContent("FOR-2026-12-18-3333");
    expect(within(card).getAllByRole("button", { name: /^Delete / })).toHaveLength(1);
  });

  it("deletes the row once confirmed, and says collection_rate_in_force in words", async () => {
    let answer = () => refusal(409, "collection_rate_in_force");
    const fetchMock = server({ "DELETE /api/v1/invoices/collection-rates/1015": () => answer() });
    renderPage();

    const card = await screen.findByTestId("collection-rates");
    await userEvent.click(
      await within(card).findByRole("button", { name: "Delete Late interest rate from Jan 1, 2027" }),
    );
    let dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Late interest rate from Jan 1, 2027: delete this row?")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(await screen.findByText(/This rate can no longer be deleted/)).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();

    answer = () => new Response(null, { status: 204 });
    await userEvent.click(within(card).getByRole("button", { name: "Delete Late interest rate from Jan 1, 2027" }));
    dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(await screen.findByText("Rate deleted")).toBeInTheDocument();
    expect(
      fetchMock.actualCalls.filter(
        ([url, init]) => String(url) === "/api/v1/invoices/collection-rates/1015" && init?.method === "DELETE",
      ),
    ).toHaveLength(2);
  });

  it("adds a future row with the kind's bounds in words, and says collection_rate_exists in words", async () => {
    let answer = () => refusal(409, "collection_rate_exists");
    const fetchMock = server({ "POST /api/v1/invoices/collection-rates": () => answer() });
    renderPage();

    const card = await screen.findByTestId("collection-rates");
    await userEvent.click(await within(card).findByRole("button", { name: "Add a rate" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("textbox", { name: "Value" })).toHaveAccessibleDescription(
      "Percent a year, 0.01 to 30, up to two decimals.",
    );
    expect(within(dialog).getByRole("textbox", { name: "From" })).toHaveAccessibleDescription(
      "1 January or 1 July, after today and after the latest printed or sent letter.",
    );
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Kind" }));
    await userEvent.click(await screen.findByRole("option", { name: "Inkassosats" }));
    expect(within(dialog).getByRole("textbox", { name: "Value" })).toHaveAccessibleDescription(
      "NOK, 100 to 5 000, up to two decimals.",
    );
    await userEvent.type(within(dialog).getByRole("textbox", { name: "From" }), "Mar 1, 2027");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Value" }), "800");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Regulation" }), "FOR-2027-01-01-1");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add a rate" }));
    expect(await screen.findByText(/A rate of this kind already takes effect on that day/)).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/collection-rates")).toEqual({
      kind: "inkassosats",
      validFrom: "2027-03-01",
      value: 800,
      sourceRef: "FOR-2027-01-01-1",
    });

    answer = () => jsonResponse(201, {});
    await userEvent.click(within(dialog).getByRole("button", { name: "Add a rate" }));
    expect(await screen.findByText("Rate added")).toBeInTheDocument();
  });

  it("puts a refused field's words on its own input", async () => {
    server({
      "POST /api/v1/invoices/collection-rates": () =>
        jsonResponse(400, {
          title: "Invalid",
          status: 400,
          errors: { validFrom: ["This rate is set per half-year: it takes effect on 1 January or 1 July"] },
        }),
    });
    renderPage();

    await userEvent.click(await screen.findByRole("button", { name: "Add a rate" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "From" }), "Mar 1, 2027");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Value" }), "12");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Regulation" }), "FOR-x");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add a rate" }));
    expect(
      await within(dialog).findByText(
        "After today and after the latest printed or sent letter; the late interest rate and the compensation on 1 January or 1 July.",
      ),
    ).toBeInTheDocument();
  });
});
