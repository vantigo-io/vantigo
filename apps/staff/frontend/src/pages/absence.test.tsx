import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { validateAbsenceSearch } from "../lib/search";
import { colleagueCalendarPeople, hrCalendarPeople, hrOnlyApprovals } from "../test/absence-fixtures";
import { jsonResponse } from "../test/api";
import { choose, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

/** The bars of one person's row, as the calendar names each to a screen reader. */
const bars = async (person: string) => {
  const row = await screen.findByRole("list", { name: person });
  return within(row)
    .getAllByRole("listitem")
    .map((bar) => bar.getAttribute("aria-label"));
};

const calendar = (month: string) => screen.findByRole("table", { name: `Absence calendar, ${month}` });

/** The calendar reads sent, as path and query. */
const calendarReads = (fetchMock: ReturnType<typeof stubStaffApi>) =>
  sent(fetchMock, "GET", /\/calendar\?/).map((one) => {
    const url = new URL(one.url, "http://localhost");
    return `${url.pathname}${url.search}`;
  });

const graceHoliday = "Grace Hopper: Holiday, Nov 2, 2026 – Nov 6, 2026";

describe("Absence — the calendar, as the server shaped it", () => {
  it("shows a colleague the dates alone: absent, without a type, a status or a share of the day", async () => {
    const fetchMock = stubStaffApi({ role: "view", absence: { calendarPeople: colleagueCalendarPeople() } });
    renderRoute("/staff/absence");

    const table = await calendar("October 2026");
    expect(await bars("Grace Hopper")).toEqual([
      "Grace Hopper: Absent · Oct 19, 2026 – Oct 23, 2026",
      "Grace Hopper: Absent · Oct 26, 2026 – Oct 27, 2026",
    ]);
    expect(await bars("Alan Turing")).toEqual(["Alan Turing: Absent · Oct 5, 2026"]);
    expect(table).not.toHaveTextContent("%");
    expect(table).not.toHaveTextContent("Approved");
    expect(table).not.toHaveTextContent("Holiday");
    // The month is one read of the calendar, within its 93 days.
    expect(calendarReads(fetchMock)).toEqual(["/api/v1/staff/calendar?from=2026-10-01&to=2026-10-31"]);
    // A colleague decides nothing: no inbox, and it is never read.
    expect(screen.queryByRole("tab", { name: "Approvals" })).not.toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/approvals/)).toHaveLength(0);
  });

  it("shows HR an HR-only type as approved leave, and a type, a share and hours only where a row carries them", async () => {
    stubStaffApi({ role: "manage", absence: { calendarPeople: hrCalendarPeople() } });
    renderRoute("/staff/absence?tab=calendar");

    expect(await bars("Grace Hopper")).toEqual([
      "Grace Hopper: Holiday · Oct 19, 2026 – Oct 23, 2026 · Approved",
      "Grace Hopper: Leave · Oct 26, 2026 – Oct 27, 2026 · Approved",
    ]);
    expect(await bars("Alan Turing")).toEqual([
      "Alan Turing: Parental leave · Oct 12, 2026 – Oct 16, 2026 · Requested · 50 % of each day",
      "Alan Turing: Doctor or dentist · Oct 21, 2026 · Registered · 2 hours",
    ]);
    expect(screen.queryByText("Pregnancy leave")).not.toBeInTheDocument();
  });

  it("shades the public holidays the server answers, and the weekends", async () => {
    stubStaffApi({
      role: "view",
      absence: { calendarPeople: colleagueCalendarPeople(), publicHolidays: ["2026-10-14"] },
    });
    renderRoute("/staff/absence");

    const table = await calendar("October 2026");
    const holiday = within(table).getByRole("columnheader", { name: "Wed, Oct 14, 2026, public holiday" });
    expect(holiday).toHaveAttribute("data-closed", "holiday");
    expect(within(table).getByRole("columnheader", { name: "Sat, Oct 17, 2026" })).toHaveAttribute(
      "data-closed",
      "weekend",
    );
    expect(within(table).getByRole("columnheader", { name: "Tue, Oct 13, 2026" })).not.toHaveAttribute("data-closed");
    expect(holiday).toHaveAttribute("scope", "col");
  });

  it("draws no bar for a row wholly outside the month", async () => {
    stubStaffApi({
      role: "view",
      read: (path) =>
        path === "/api/v1/staff/calendar"
          ? jsonResponse(200, {
              from: "2026-10-01",
              to: "2026-10-31",
              publicHolidays: [],
              people: [
                {
                  person: { id: 102, displayName: "Grace Hopper", employeeNumber: "E-2" },
                  absences: [
                    { id: 631, personId: 102, firstDay: "2026-09-20", lastDay: "2026-09-25", shownAs: "absent" },
                    { id: 632, personId: 102, firstDay: "2026-10-30", lastDay: "2026-11-04", shownAs: "absent" },
                  ],
                },
              ],
            })
          : undefined,
    });
    renderRoute("/staff/absence");

    expect(await bars("Grace Hopper")).toEqual(["Grace Hopper: Absent · Oct 30, 2026 – Nov 4, 2026"]);
  });

  it("pages a month at a time, each month read on its own", async () => {
    const fetchMock = stubStaffApi({ role: "view", absence: { calendarPeople: colleagueCalendarPeople() } });
    const { router } = renderRoute("/staff/absence");

    await calendar("October 2026");
    await userEvent.click(screen.getByRole("button", { name: "Next month" }));

    await calendar("November 2026");
    await waitFor(() =>
      expect(calendarReads(fetchMock)).toContain("/api/v1/staff/calendar?from=2026-11-01&to=2026-11-30"),
    );
    expect(router.state.location.search).toMatchObject({ month: "2026-11" });
    // No bar of October's is drawn in November.
    await waitFor(() => expect(screen.queryByRole("listitem")).not.toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "Previous month" }));
    await userEvent.click(screen.getByRole("button", { name: "Previous month" }));
    await calendar("September 2026");
  });

  it("narrows to a department, which the server judges", async () => {
    const fetchMock = stubStaffApi({ role: "manage", absence: { calendarPeople: hrCalendarPeople() } });
    const { router } = renderRoute("/staff/absence?tab=calendar");

    await calendar("October 2026");
    await choose(document.body, "Department", "Operations");

    await waitFor(() =>
      expect(calendarReads(fetchMock)).toContain(
        "/api/v1/staff/calendar?from=2026-10-01&to=2026-10-31&departmentId=22",
      ),
    );
    expect(router.state.location.search).toMatchObject({ departmentId: 22 });
  });

  it("offers no department filter to a caller who reads no structure", async () => {
    stubStaffApi({ role: "personalDataOnly", absence: { calendarPeople: hrCalendarPeople(), approvals: [] } });
    renderRoute("/staff/absence?tab=calendar");

    await calendar("October 2026");
    expect(screen.queryByRole("combobox", { name: "Department" })).not.toBeInTheDocument();
  });
});

describe("Absence — the approvals inbox", () => {
  it("opens on the inbox for a caller who decides, each request approved inline", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/absence");

    expect(await screen.findByRole("tab", { name: "Approvals", selected: true })).toBeInTheDocument();
    const request = await screen.findByRole("article", { name: graceHoliday });
    expect(within(request).getByText("5 working days")).toBeInTheDocument();
    await userEvent.click(within(request).getByRole("button", { name: "Approve" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/absences\/601\/approve$/)).toHaveLength(1));
    expect(await screen.findByText("Nothing waits for your decision")).toBeInTheDocument();
  });

  it("rejects only with a reason", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/absence?tab=approvals");

    const request = await screen.findByRole("article", { name: graceHoliday });
    await userEvent.click(within(request).getByRole("button", { name: "Reject" }));
    await userEvent.click(within(request).getByRole("button", { name: "Reject request" }));
    expect(await within(request).findByText("Say why the request is rejected")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/reject$/)).toHaveLength(0);

    await retype(within(request).getByRole("textbox", { name: "Reason" }), "Release week");
    await userEvent.click(within(request).getByRole("button", { name: "Reject request" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/absences\/601\/reject$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/absences\/601\/reject$/)[0].body).toEqual({ note: "Release week" });
  });

  it("says in words that somebody was quicker, and reads the inbox again", async () => {
    const fetchMock = stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "POST" && path.endsWith("/approve")
          ? staffProblem(409, {
              code: "absence_not_requested",
              title: "Absence not requested",
              detail: "The server's English.",
            })
          : undefined,
    });
    renderRoute("/staff/absence?tab=approvals");

    const request = await screen.findByRole("article", { name: graceHoliday });
    expect(sent(fetchMock, "GET", /\/approvals/)).toHaveLength(1);
    await userEvent.click(within(request).getByRole("button", { name: "Approve" }));

    expect(
      await screen.findByText("This request is no longer waiting: somebody decided it, or it was withdrawn."),
    ).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
    await waitFor(() => expect(sent(fetchMock, "GET", /\/approvals/)).toHaveLength(2));
  });

  it("words an overlap found at approval as the decider's, not the person's", async () => {
    stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "POST" && path.endsWith("/approve")
          ? staffProblem(409, { code: "absence_overlap", title: "Absence overlap", detail: "The server's English." })
          : undefined,
    });
    renderRoute("/staff/absence?tab=approvals");

    const request = await screen.findByRole("article", { name: graceHoliday });
    await userEvent.click(within(request).getByRole("button", { name: "Approve" }));

    expect(await screen.findByText("The person already has an absence on one of these days.")).toBeInTheDocument();
    expect(screen.queryByText("You already have an absence on one of these days.")).not.toBeInTheDocument();
  });

  it("says a request that is gone is no longer there, never the client's HTTP status", async () => {
    stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "POST" && path.endsWith("/approve") ? new Response(null, { status: 404 }) : undefined,
    });
    renderRoute("/staff/absence?tab=approvals");

    const request = await screen.findByRole("article", { name: graceHoliday });
    await userEvent.click(within(request).getByRole("button", { name: "Approve" }));

    expect(await screen.findByText("This is no longer there.")).toBeInTheDocument();
    expect(screen.queryByText(/HTTP 404/)).not.toBeInTheDocument();
  });

  it("gives a holder of staff:personal-data alone the inbox the server answers, an HR-only type by its name", async () => {
    stubStaffApi({ role: "personalDataOnly", absence: { approvals: hrOnlyApprovals() } });
    renderRoute("/staff/absence");

    expect(
      await screen.findByRole("article", { name: "Grace Hopper: Pregnancy leave, Feb 1, 2027 – Apr 30, 2027" }),
    ).toBeInTheDocument();
  });

  it("says so when the inbox cannot be read", async () => {
    stubStaffApi({
      role: "manage",
      read: (path) =>
        path === "/api/v1/staff/approvals" ? staffProblem(500, { title: "Server error", detail: "Boom" }) : undefined,
    });
    renderRoute("/staff/absence?tab=approvals");

    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load the requests");
    expect(screen.queryByText("Nothing waits for your decision")).not.toBeInTheDocument();
  });

  it("draws only what the inbox answers: an empty one hints at nothing", async () => {
    const fetchMock = stubStaffApi({ role: "manage", absence: { approvals: [] } });
    renderRoute("/staff/absence?tab=approvals");

    expect(await screen.findByText("Nothing waits for your decision")).toBeInTheDocument();
    expect(screen.queryByRole("article")).not.toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/absences(\?|$)/)).toHaveLength(0);
  });
});

describe("Absence — the URL", () => {
  it("keeps the tab, the month and the department, and drops what a hand-edited link invents", () => {
    expect(validateAbsenceSearch({ tab: "approvals", month: "2026-11", departmentId: "22" })).toEqual({
      tab: "approvals",
      month: "2026-11",
      departmentId: 22,
    });
    expect(validateAbsenceSearch({ tab: "people", month: "2026-13", departmentId: "-1" })).toEqual({});
    expect(validateAbsenceSearch({ month: "november" })).toEqual({});
  });
});
