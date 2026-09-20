import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { type Claim, expenseClaimQueryOptions } from "../api/claims";
import type { Expense } from "../api/entries";
import { jsonResponse, problemResponse, sent } from "../test/api";
import {
  claim as aClaim,
  claimCapabilities,
  meta,
  mileage,
  outlay,
  perDiemLine,
  perDiemRates,
  projectOptions,
  rates,
} from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubExpensesApi } from "../test/server";

const allRates = [...rates, ...perDiemRates];

/** Every request of one method, newest last, so a sequence can be read back. */
const allSent = (fetchMock: ReturnType<typeof stubExpensesApi>, method: string) =>
  fetchMock.actualCalls
    .filter(([, request]) => (request?.method ?? "GET") === method)
    .map(([url, request]) => ({
      url: String(url),
      body: request?.body ? JSON.parse(String(request.body)) : undefined,
    }));

const openClaim = (claim: Claim, entries: Expense[] = [], server = {}) => {
  const fetchMock = stubExpensesApi({ claims: [claim], entries, rates: allRates, ...server });
  renderRoute(`/expenses/claims/${claim.id}`);
  return fetchMock;
};

const setDay = async (dialog: HTMLElement, label: string, value: string) => {
  const input = within(dialog).getByRole("textbox", { name: label });
  await userEvent.clear(input);
  await userEvent.type(input, value);
};

const setTime = async (dialog: HTMLElement, label: string, value: string) => {
  const input = within(dialog).getByLabelText(label);
  await userEvent.clear(input);
  await userEvent.type(input, value);
};

const dayRow = async (label: string) => (await screen.findByText(label)).closest("[data-per-diem-day]") as HTMLElement;

describe("ClaimPage", () => {
  it("writes the trip out: purpose, destination, the dates in the installation's zone and the totals", async () => {
    openClaim(aClaim(), [perDiemLine()]);

    expect(await screen.findByText("Montasje hos kunden")).toBeInTheDocument();
    expect(screen.getByText("Bergen")).toBeInTheDocument();
    // 06:00Z on 9 March is 07:00 in Oslo, which is what the traveller typed.
    expect(screen.getByTestId("claim-trip")).toHaveTextContent("7:00 AM");
    expect(screen.getByTestId("claim-trip")).toHaveTextContent("Times are in Europe/Oslo");
    expect(screen.getByTestId("claim-totals")).toHaveTextContent("1,012.00");
  });

  it("sends a winter wall-clock time as an instant with the installation's winter offset", async () => {
    // The browser's own zone is not the company's. The form asks for a day and
    // a time, and what goes on the wire is the instant that clock names in
    // `meta.timeZone` — whatever zone the test process itself runs in. A
    // departure at half past midnight is the sort that slides to the day
    // before if the offset is guessed.
    const fetchMock = openClaim(aClaim());
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    await setTime(dialog, "Time of departure", "00:30");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(allSent(fetchMock, "PUT")).toHaveLength(1));
    expect(allSent(fetchMock, "PUT")[0].body).toMatchObject({
      departureAt: "2026-03-09T00:30:00+01:00",
      returnAt: "2026-03-11T16:00:00+01:00",
      revision: 1,
    });
  });

  it("sends a summer wall-clock time with the installation's summer offset", async () => {
    const fetchMock = openClaim(aClaim());
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    await setDay(dialog, "Day of departure", "Jul 1, 2026");
    await setDay(dialog, "Day of return", "Jul 3, 2026");
    await setTime(dialog, "Time of departure", "00:30");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(allSent(fetchMock, "PUT")).toHaveLength(1));
    expect(allSent(fetchMock, "PUT")[0].body).toMatchObject({
      departureAt: "2026-07-01T00:30:00+02:00",
      returnAt: "2026-07-03T16:00:00+02:00",
    });
  });

  it("asks for a day rate and a currency once the trip is abroad, and refetches the repriced days", async () => {
    const fetchMock = openClaim(aClaim(), [perDiemLine()]);
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    await userEvent.click(within(dialog).getByRole("radio", { name: "Abroad" }));
    // Both are required with it, and the form says so before the server does.
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText("Give the day rate, more than zero")).toBeInTheDocument();

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Day rate abroad" }), "90");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Currency abroad" }), "EUR");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(allSent(fetchMock, "PUT")).toHaveLength(1));
    expect(allSent(fetchMock, "PUT")[0].body).toMatchObject({ abroad: true, abroadDayRate: 90, abroadCurrency: "EUR" });
    // The server repriced the day in the same transaction, so the page reads
    // the claim again rather than showing the old amount.
    const section = await screen.findByTestId("per-diem-section");
    await waitFor(() => expect(within(section).queryByText(/1,012\.00/)).not.toBeInTheDocument());
    expect(within(section).getAllByText("€90.00").length).toBeGreaterThan(0);
  });

  it("reads the trip again after a conflict so the next save from the same form lands", async () => {
    // Somebody else saved the trip while this form was open. The form keeps
    // what was typed, says so, and re-reads the revision — a form that could
    // only ever 409 again is a trap.
    const trip = aClaim();
    let firstSave = true;
    const fetchMock = openClaim(trip, [], {
      write: (method: string, path: string) => {
        if (method !== "PUT" || path !== "/api/v1/expenses/claims/1012" || !firstSave) return undefined;
        firstSave = false;
        trip.revision = 2;
        return jsonResponse(409, { title: "The travel claim has moved on", status: 409 });
      },
    });
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    const purpose = within(dialog).getByRole("textbox", { name: "What the trip was for" });
    await userEvent.clear(purpose);
    await userEvent.type(purpose, "Montasje i Bergen");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText(/Read it again and retry/)).toBeInTheDocument();
    expect(purpose).toHaveValue("Montasje i Bergen");

    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(allSent(fetchMock, "PUT")).toHaveLength(2));
    expect(allSent(fetchMock, "PUT")[1].body).toMatchObject({ purpose: "Montasje i Bergen", revision: 2 });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Edit the trip" })).not.toBeInTheDocument());
  });

  it("refuses to strand a per diem day, and says which day it is", async () => {
    const fetchMock = openClaim(aClaim(), [perDiemLine({ entryDate: "2026-03-11" })]);
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    const returnDay = within(dialog).getByRole("textbox", { name: "Day of return" });
    await userEvent.clear(returnDay);
    await userEvent.type(returnDay, "Mar 10, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText(/2026-03-11/)).toBeInTheDocument();
    expect(allSent(fetchMock, "PUT")).toHaveLength(1);
  });

  it("suggests the trip's days once it is told whether the traveller slept away", async () => {
    const fetchMock = openClaim(aClaim(), [perDiemLine({ entryDate: "2026-03-09" })]);
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Suggest days" }));
    const dialog = await screen.findByRole("dialog", { name: "Suggest the trip's days" });
    await userEvent.click(within(dialog).getByRole("radio", { name: "Yes" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Suggest" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({
        url: `/api/v1/expenses/claims/1012/per-diem-suggestion`,
        body: { overnight: true },
      }),
    );

    // Three 24-hour periods' worth of trip: 9, 10 and 11 March. The 9th is
    // already recorded, so it is marked and left unticked.
    const suggested = await within(dialog).findByRole("table", { name: "Suggested days" });
    expect(within(suggested).getByText("Already added")).toBeInTheDocument();
    expect(within(dialog).getByRole("checkbox", { name: "Add Mar 9, 2026" })).not.toBeChecked();
    expect(within(dialog).getByRole("checkbox", { name: "Add Mar 10, 2026" })).toBeChecked();

    await userEvent.click(within(dialog).getByRole("button", { name: "Add 2 days" }));

    await waitFor(() => {
      const creates = allSent(fetchMock, "POST").filter((one) => one.url === "/api/v1/expenses/entries");
      expect(creates).toHaveLength(2);
      expect(creates[0].body).toMatchObject({ kind: "per_diem", claimId: 1012, entryDate: "2026-03-10" });
      expect(creates[1].body).toMatchObject({ kind: "per_diem", claimId: 1012, entryDate: "2026-03-11" });
    });
  });

  it("stops adding suggested days at the first refusal and says why", async () => {
    const fetchMock = openClaim(aClaim(), [], {
      write: (method: string, path: string, body: { entryDate?: string }) =>
        method === "POST" && path === "/api/v1/expenses/entries" && body.entryDate === "2026-03-10"
          ? problemResponse(400, "Invalid expense", {
              claimId: ["This travel claim changed while the expense was being recorded; read it again and retry"],
            })
          : undefined,
    });
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Suggest days" }));
    const dialog = await screen.findByRole("dialog", { name: "Suggest the trip's days" });
    await userEvent.click(within(dialog).getByRole("radio", { name: "Yes" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Suggest" }));
    await within(dialog).findByRole("table", { name: "Suggested days" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Add 3 days" }));

    expect(await within(dialog).findByText(/read it again and retry/)).toBeInTheDocument();
    const creates = allSent(fetchMock, "POST").filter((one) => one.url === "/api/v1/expenses/entries");
    expect(creates).toHaveLength(2);
  });

  it("names the day that stopped the add, and leaves the ones that landed out of a second try", async () => {
    const fetchMock = openClaim(aClaim(), [], {
      write: (method: string, path: string, body: { entryDate?: string }) =>
        method === "POST" && path === "/api/v1/expenses/entries" && body.entryDate === "2026-03-10"
          ? problemResponse(400, "Invalid expense", {
              claimId: ["This travel claim changed while the expense was being recorded; read it again and retry"],
            })
          : undefined,
    });
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Suggest days" }));
    const dialog = await screen.findByRole("dialog", { name: "Suggest the trip's days" });
    await userEvent.click(within(dialog).getByRole("radio", { name: "Yes" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Suggest" }));
    await within(dialog).findByRole("table", { name: "Suggested days" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Add 3 days" }));

    // Which day stopped it, in the reader's own language — the server's
    // sentence names no date at all for this refusal.
    expect(await within(dialog).findByText(/Mar 10, 2026 could not be added/)).toBeInTheDocument();
    expect(within(dialog).getByText(/read it again and retry/)).toBeInTheDocument();
    // The 9th landed, so it is marked and unticked and the button offers the
    // two that are left: pressing it again never re-posts a recorded day.
    expect(await within(dialog).findByRole("button", { name: "Add 2 days" })).toBeInTheDocument();
    expect(within(dialog).getByRole("checkbox", { name: "Add Mar 9, 2026" })).not.toBeChecked();
    const creates = allSent(fetchMock, "POST").filter((one) => one.url === "/api/v1/expenses/entries");
    expect(creates).toHaveLength(2);
  });

  it("saves a meal the moment it is ticked and shows the amount the server worked out", async () => {
    const fetchMock = openClaim(aClaim(), [perDiemLine()]);
    const row = await dayRow("Overnight, hotel");
    expect(row).toHaveTextContent("1,012.00");

    await userEvent.click(within(row).getByRole("checkbox", { name: "Breakfast covered on Mar 9, 2026" }));

    await waitFor(() => expect(allSent(fetchMock, "PUT")).toHaveLength(1));
    expect(allSent(fetchMock, "PUT")[0].body).toMatchObject({
      kind: "per_diem",
      claimId: 1012,
      entryDate: "2026-03-09",
      perDiemType: "overnight_hotel",
      breakfastCovered: true,
      lunchCovered: false,
      dinnerCovered: false,
      revision: 1,
    });
    // 1012 less 20 % is 809.60 — the server's figure, never the client's.
    expect(await within(await dayRow("Overnight, hotel")).findByText(/809\.60/)).toBeInTheDocument();
  });

  it("carries the revision the first answer gave into the second write on the same row", async () => {
    // `frozenReads` makes every refetch answer the trip as it stood before the
    // first tick. A row that took its revision from what it was handed would
    // send the stale 1 a second time and earn a 409; the row's revision is
    // the one its own last answer gave.
    const fetchMock = openClaim(aClaim(), [perDiemLine()], { frozenReads: true });
    const row = await dayRow("Overnight, hotel");

    await userEvent.click(within(row).getByRole("checkbox", { name: "Breakfast covered on Mar 9, 2026" }));
    await waitFor(() => expect(allSent(fetchMock, "PUT")).toHaveLength(1));
    await userEvent.click(
      within(await dayRow("Overnight, hotel")).getByRole("checkbox", { name: "Lunch covered on Mar 9, 2026" }),
    );

    await waitFor(() => expect(allSent(fetchMock, "PUT")).toHaveLength(2));
    expect(allSent(fetchMock, "PUT")[1].body).toMatchObject({
      revision: 2,
      breakfastCovered: true,
      lunchCovered: true,
    });
  });

  it("shows the refusal on the type select when the table prices no such day", async () => {
    const fetchMock = openClaim(aClaim(), [perDiemLine()]);
    const row = await dayRow("Overnight, hotel");

    await userEvent.click(within(row).getByRole("combobox", { name: "Kind of day for Mar 9, 2026" }));
    await userEvent.click(await screen.findByRole("option", { name: "Overnight, other lodging" }));

    await waitFor(() => expect(allSent(fetchMock, "PUT")).toHaveLength(1));
    expect(await screen.findByText(/No per_diem_overnight_other rate applies on 2026-03-09/)).toBeInTheDocument();
  });

  it("puts a meal's refusal under the meal that caused it, not under the kind of day", async () => {
    // The table prices no breakfast deduction on this day, so ticking it is a
    // 400 on `breakfastCovered`. The sentence belongs beside that checkbox,
    // not several columns away under the type select.
    openClaim(aClaim(), [perDiemLine()], {
      rates: [...rates, ...perDiemRates.filter((rate) => rate.kind !== "meal_breakfast_percent")],
    });
    const row = await dayRow("Overnight, hotel");

    await userEvent.click(within(row).getByRole("checkbox", { name: "Breakfast covered on Mar 9, 2026" }));

    const refusal = await within(await dayRow("Overnight, hotel")).findByText(
      /No meal_breakfast_percent rate applies on 2026-03-09/,
    );
    expect(refusal).toBeInTheDocument();
    expect(
      within(await dayRow("Overnight, hotel")).getByRole("combobox", { name: "Kind of day for Mar 9, 2026" }),
    ).not.toHaveAttribute("aria-invalid", "true");
  });

  it("says the trip moved on when a row's save is a conflict, and reads it again", async () => {
    const fetchMock = openClaim(aClaim(), [perDiemLine()], {
      write: (method: string, path: string) =>
        method === "PUT" && path === "/api/v1/expenses/entries/801"
          ? jsonResponse(409, { title: "The expense has moved on", status: 409 })
          : undefined,
    });
    const row = await dayRow("Overnight, hotel");
    const readsBefore = fetchMock.actualCalls.filter(([url]) => String(url).includes("/claims/1012")).length;

    await userEvent.click(within(row).getByRole("checkbox", { name: "Lunch covered on Mar 9, 2026" }));

    // A 409 is not a field error: it belongs in the row's own message, with
    // the sentence that tells the traveller what to do about it.
    expect(
      await within(await dayRow("Overnight, hotel")).findByText(/Somebody changed this travel claim/),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(fetchMock.actualCalls.filter(([url]) => String(url).includes("/claims/1012")).length).toBeGreaterThan(
        readsBefore,
      ),
    );
  });

  it("removes a per diem day from a button named after its own day", async () => {
    const fetchMock = openClaim(aClaim(), [perDiemLine()]);
    const row = await dayRow("Overnight, hotel");

    await userEvent.click(within(row).getByRole("button", { name: "Remove the per diem for Mar 9, 2026" }));
    const confirm = await screen.findByRole("dialog", { name: "Delete the expense?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Delete" }));

    await waitFor(() =>
      expect(allSent(fetchMock, "DELETE").map((one) => one.url)).toContain("/api/v1/expenses/entries/801"),
    );
  });

  it("records a mileage line on the trip, booked on the trip's project", async () => {
    const fetchMock = openClaim(aClaim({ project: { id: 1001, code: "KVEM1000", name: "Kverneland web" } }), [], {
      projects: projectOptions,
    });
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Add mileage" }));
    const dialog = await screen.findByRole("dialog", { name: "New expense" });
    // The line takes the trip's project: there is no picker, only what it is
    // booked on and whether it is billed on to the customer.
    expect(within(dialog).getByText("Booked on KVEM1000 · Kverneland web")).toBeInTheDocument();
    expect(within(dialog).queryByRole("combobox", { name: "Project" })).not.toBeInTheDocument();
    // A trip's expenses are submitted with the trip, never one at a time.
    expect(within(dialog).queryByRole("button", { name: "Save and submit" })).not.toBeInTheDocument();

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Til anlegget");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Distance in kilometres" }), "120");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/entries"));
    expect(sent(fetchMock, "POST").body).toMatchObject({
      kind: "mileage",
      claimId: 1012,
      description: "Til anlegget",
      distanceKm: 120,
    });
    expect(await screen.findByText("Til anlegget")).toBeInTheDocument();
  });

  it("leaves every project control out where this installation has no projects", async () => {
    // The very same trip — one that *carries* a stored project — so the
    // assertions below are about the projects gate and not about a fixture
    // that happens to be booked on nothing.
    openClaim(aClaim({ project: { id: 1001, code: "KVEM1000", name: "Kverneland web" } }), [], {
      meta: meta({ projectsAvailable: false }),
      projects: projectOptions,
    });
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Add an outlay" }));
    const lineDialog = await screen.findByRole("dialog", { name: "New expense" });
    expect(within(lineDialog).queryByText(/Booked on/)).not.toBeInTheDocument();
    expect(within(lineDialog).queryByRole("switch", { name: "Billable" })).not.toBeInTheDocument();
    await userEvent.click(within(lineDialog).getByRole("button", { name: "Cancel" }));

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const tripDialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    expect(within(tripDialog).queryByRole("combobox", { name: "Project" })).not.toBeInTheDocument();
  });

  it("shows every project control on the same trip where this installation has projects", async () => {
    openClaim(aClaim({ project: { id: 1001, code: "KVEM1000", name: "Kverneland web" } }), [], {
      projects: projectOptions,
    });
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Add an outlay" }));
    const lineDialog = await screen.findByRole("dialog", { name: "New expense" });
    expect(within(lineDialog).getByText("Booked on KVEM1000 · Kverneland web")).toBeInTheDocument();
    expect(within(lineDialog).getByRole("switch", { name: "Billable" })).toBeInTheDocument();
    await userEvent.click(within(lineDialog).getByRole("button", { name: "Cancel" }));

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const tripDialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    expect(within(tripDialog).getByRole("combobox", { name: "Project" })).toBeInTheDocument();
  });

  it("keeps a project the trip carries but nobody may book on any more", async () => {
    openClaim(aClaim({ project: { id: 4004, code: "OLD1000", name: "Completed job" } }), [], {
      projects: projectOptions,
    });
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    // The save grandfathers a link the trip already carries, so the picker has
    // to show it; without it the form would tell somebody their booked trip is
    // unbooked.
    expect(within(dialog).getByRole("combobox", { name: "Project" })).toHaveValue(
      "OLD1000 · Completed job (no longer bookable)",
    );
  });

  it("re-points the trip's lines when the trip's project changes", async () => {
    const fetchMock = openClaim(
      aClaim({ project: { id: 1001, code: "KVEM1000", name: "Kverneland web" } }),
      [mileage({ id: 601, claimId: 1012, entryDate: "2026-03-09", description: "Til anlegget" })],
      { projects: projectOptions },
    );
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Project" }));
    await userEvent.click(await screen.findByRole("option", { name: "INTERN · Internal" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(allSent(fetchMock, "PUT")[0]?.body).toMatchObject({ projectId: 1002 }));
    // Every line is the trip's project's, so the badge on the page moves with it.
    expect(await screen.findByText("INTERN · Internal")).toBeInTheDocument();
  });

  it("clears the day rate and the currency when a trip stops being abroad", async () => {
    const fetchMock = openClaim(aClaim({ abroad: true, abroadDayRate: 90, abroadCurrency: "EUR" }), [
      perDiemLine({ currency: "EUR", grossAmount: 90, rate: 90 }),
    ]);
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    await userEvent.click(within(dialog).getByRole("radio", { name: "Domestic" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(allSent(fetchMock, "PUT")).toHaveLength(1));
    const body = allSent(fetchMock, "PUT")[0].body as Record<string, unknown>;
    expect(body.abroad).toBe(false);
    // Absent, never `null` or `0`: the contract refuses either on a domestic trip.
    expect("abroadDayRate" in body).toBe(false);
    expect("abroadCurrency" in body).toBe(false);
  });

  it("keeps a wall clock that falls in the hour the clocks go forward", async () => {
    // 2026-03-29 02:30 does not exist in Oslo. The form still names a real
    // instant, deterministically, rather than throwing or writing a day off.
    const fetchMock = openClaim(aClaim());
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    await setDay(dialog, "Day of departure", "Mar 29, 2026");
    await setTime(dialog, "Time of departure", "02:30");
    await setDay(dialog, "Day of return", "Mar 30, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(allSent(fetchMock, "PUT")).toHaveLength(1));
    expect(allSent(fetchMock, "PUT")[0].body).toMatchObject({
      departureAt: "2026-03-29T02:30:00+01:00",
      returnAt: "2026-03-30T16:00:00+02:00",
    });
  });

  it("waits for the installation's zone before it shows a trip at all", async () => {
    // A cold deep link: the claim lands before `/meta`. A page that read a
    // wall clock out of a guessed zone would capture 06:00 for an Oslo 07:00
    // departure and save the trip an hour early, with no refusal anywhere.
    const fetchMock = stubExpensesApi({
      claims: [aClaim()],
      entries: [],
      rates: allRates,
      hold: ["/api/v1/expenses/meta"],
    });
    const { queryClient } = renderRoute("/expenses/claims/1012");

    // The claim is *answered and cached* and the page still shows nothing:
    // there is no zone yet to write a wall clock in.
    await waitFor(() => expect(queryClient.getQueryData(expenseClaimQueryOptions(1012).queryKey)).toBeDefined());
    expect(screen.queryByText("Montasje hos kunden")).not.toBeInTheDocument();

    fetchMock.release();
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Edit the trip" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the trip" });
    expect(within(dialog).getByLabelText("Time of departure")).toHaveValue("07:00");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(allSent(fetchMock, "PUT")).toHaveLength(1));
    expect(allSent(fetchMock, "PUT")[0].body).toMatchObject({
      departureAt: "2026-03-09T07:00:00+01:00",
      returnAt: "2026-03-11T16:00:00+01:00",
    });
  });

  it("offers no way to add anything once the trip holds the two hundred it may", async () => {
    const lines = Array.from({ length: 200 }, (_, index) =>
      mileage({ id: 1000 + index, claimId: 1012, entryDate: "2026-03-09", description: `Tur ${index}` }),
    );
    openClaim(aClaim(), lines);
    await screen.findByText("Montasje hos kunden");

    expect(screen.getByRole("button", { name: "Suggest days" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add mileage" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add an outlay" })).toBeDisabled();
    expect(
      screen.getAllByText("This travel claim already holds the 200 expenses a travel claim may hold.").length,
    ).toBeGreaterThan(1);
  });

  it("submits the whole trip as one unit", async () => {
    const fetchMock = openClaim(aClaim(), [perDiemLine()]);
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Submit claim" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/expenses/submit", body: { claimIds: [1012] } }),
    );
    expect(await screen.findByText("Sent for approval")).toBeInTheDocument();
  });

  it("shows a refusal the submit answered on claimIds, against the line it names", async () => {
    // The claim's own refusals arrive on `claimIds`, a key the package used
    // to ignore entirely — the trip's explanation was fetched and dropped.
    openClaim(aClaim(), [perDiemLine({ id: 801 })], {
      write: (method: string, path: string) =>
        method === "POST" && path === "/api/v1/expenses/submit"
          ? problemResponse(400, "Invalid submission", {
              claimIds: [
                "Travel claim 1012 cannot be submitted: Expense 801 cannot be priced: No per_diem_overnight_other rate applies on 2026-03-09",
              ],
            })
          : undefined,
    });
    await screen.findByText("Montasje hos kunden");

    await userEvent.click(screen.getByRole("button", { name: "Submit claim" }));

    expect(await screen.findByTestId("claim-refusals")).toHaveTextContent("cannot be priced");
    expect(await within(await dayRow("Overnight, hotel")).findByText(/cannot be priced/)).toBeInTheDocument();
  });

  it("writes a trip nobody may change out in full, with no controls at all", async () => {
    openClaim(
      aClaim({
        status: "approved",
        capabilities: claimCapabilities(),
        decision: { status: "approved", at: "2026-03-12T09:00:00Z" },
      }),
      [outlay({ id: 502, claimId: 1012, description: "Hotell", status: "approved" })],
    );

    expect(await screen.findByText("This travel claim can no longer be changed.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit the trip" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Suggest days" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Submit claim" })).not.toBeInTheDocument();
    expect(screen.getByRole("table", { name: "The trip's expenses" })).toHaveTextContent("Hotell");
  });

  it("says so, rather than failing, when the claim is not the caller's to see", async () => {
    stubExpensesApi({ claims: [], entries: [], rates: allRates });
    renderRoute("/expenses/claims/4242");

    expect(await screen.findByText("This travel claim is not here")).toBeInTheDocument();
  });

  it("lists the mileage the trip holds, with what it was priced at", async () => {
    openClaim(aClaim(), [mileage({ id: 601, claimId: 1012, entryDate: "2026-03-09", description: "Til anlegget" })]);

    const section = await screen.findByRole("table", { name: "Mileage" });
    expect(section).toHaveTextContent("Til anlegget");
    expect(section).toHaveTextContent("636.00");
  });
});
