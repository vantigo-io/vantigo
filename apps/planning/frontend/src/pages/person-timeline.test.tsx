import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { addDays } from "../lib/weeks";
import { allocation, allocationWithTerms, HANDOVER, RELEASE, TODAY, week, weekEnd, wishes } from "../test/fixtures";
import { shownDate, shownStamp } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubPlanningApi } from "../test/server";

const ending = addDays(TODAY, 20);
const expiry = addDays(TODAY, 30);

const flagged = () =>
  stubPlanningApi({
    role: "view",
    people: [
      {
        id: 1,
        displayName: "Ada Lovelace",
        employeeNumber: "E-1",
        department: { id: 10, name: "Engineering" },
        employed: [{ from: addDays(TODAY, -400), to: ending }],
      },
    ],
    allocations: [
      allocation({ id: 501, personId: 1, projectId: 100, toDate: weekEnd(11) }),
      allocation({ id: 502, personId: 1, projectId: 200, status: "tentative", toDate: weekEnd(3) }),
    ],
    away: { 1: [{ from: week(1), to: addDays(week(1), 2) }] },
    flags: [
      { allocationId: 501, code: "beyond_employment", date: ending },
      { allocationId: 501, code: "certificate_expires", date: expiry },
    ],
  });

describe("PersonTimelinePage — a person's timeline", () => {
  it("shows the employment, the days away, the bars and both flags", async () => {
    flagged();
    renderRoute("/planning/people/1");

    expect(await screen.findByRole("heading", { name: "Timeline — Ada Lovelace" })).toBeInTheDocument();
    expect(await screen.findByRole("list", { name: "Employment" })).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Days away" })).toBeInTheDocument();
    const bars = screen.getByRole("list", { name: "Allocations" });
    expect(within(bars).getAllByRole("listitem")).toHaveLength(2);

    const row = (
      await within(screen.getByRole("table", { name: "Allocations" })).findByText("P-100 Harbour app")
    ).closest("tr") as HTMLElement;
    expect(within(row).getByText(/^After the employment ends/)).toBeInTheDocument();
    expect(within(row).getByText(/^Certificate expires/)).toBeInTheDocument();
    const tentative = (screen.getByText("P-200 Tender bid").closest("tr") as HTMLElement).textContent;
    expect(tentative).toContain("Tentative");
  });

  it("is the person's confirmed rows and nothing else when they read their own", async () => {
    stubPlanningApi({
      role: "access",
      me: 1,
      allocations: [
        allocation({ id: 501, personId: 1, projectId: 100 }),
        allocation({ id: 502, personId: 1, projectId: 200, status: "tentative" }),
      ],
      away: { 1: [{ from: week(1), to: week(1) }] },
    });
    renderRoute("/planning/people/1");

    expect(await screen.findByText("P-100 Harbour app")).toBeInTheDocument();
    expect(screen.queryByText("P-200 Tender bid")).not.toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Employment" })).not.toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Days away" })).not.toBeInTheDocument();
    expect(screen.getByText(/Your employment and your absence are on My page in Staff/)).toBeInTheDocument();
  });

  it("says a person the plan does not answer for is not in it, without an error", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning/people/99");
    expect(await screen.findByText("Not in the plan for these weeks")).toBeInTheDocument();
    expect(screen.queryByText("Could not load the timeline")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("PersonTimelinePage — terms and wishes", () => {
  it("marks the earliest release and the handover, lists them in the table, and shows the open wishes", async () => {
    stubPlanningApi({
      allocations: [
        allocationWithTerms(),
        allocation({ id: 502, personId: 1, projectId: 200, status: "tentative", toDate: weekEnd(3) }),
      ],
    });
    renderRoute(`/planning/people/1?to=${weekEnd(25)}`);

    const marks = await screen.findByRole("list", { name: "Earliest release and handover" });
    const items = within(marks).getAllByRole("listitem");
    expect(items.map((item) => item.getAttribute("title"))).toEqual([
      `P-100 Harbour app: Earliest release ${shownDate(RELEASE)}`,
      `P-100 Harbour app: Handover from ${shownDate(HANDOVER)}`,
    ]);

    const table = screen.getByRole("table", { name: "Allocations" });
    const headers = within(table)
      .getAllByRole("columnheader")
      .map((cell) => cell.textContent);
    expect(headers).toEqual(["Project", "Period", "Load", "Status", "Earliest release", "Handover from", "Flags"]);
    const termed = within(table).getByText("P-100 Harbour app").closest("tr") as HTMLElement;
    const cells = within(termed).getAllByRole("cell");
    expect(cells[4]).toHaveTextContent(shownDate(RELEASE));
    expect(cells[5]).toHaveTextContent(shownDate(HANDOVER));
    const plain = within(within(table).getByText("P-200 Tender bid").closest("tr") as HTMLElement).getAllByRole("cell");
    expect(plain[4]).toHaveTextContent("—");
    expect(plain[5]).toHaveTextContent("—");

    const open = screen.getByRole("list", { name: "Open wishes" });
    const [ada] = wishes();
    expect(within(open).getByRole("listitem")).toHaveTextContent(
      `Off P-100 Harbour app from ${shownDate(ada.fromDate)}Not seen yet`,
    );
  });

  it("says when a planner saw a wish", async () => {
    const [, linus] = wishes();
    stubPlanningApi({ allocations: [allocation({ id: 503, personId: 3, projectId: 100 })] });
    renderRoute("/planning/people/3");
    const open = await screen.findByRole("list", { name: "Open wishes" });
    expect(within(open).getByRole("listitem")).toHaveTextContent(
      `Something new from ${shownDate(linus.fromDate)}Seen ${shownStamp(linus.seenAt ?? "")}`,
    );
  });

  it("shows a viewer neither the wishes nor any mark where no term binds", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning/people/1");
    expect(await screen.findByText("P-100 Harbour app")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Open wishes" })).not.toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Earliest release and handover" })).not.toBeInTheDocument();
  });
});

describe("PersonTimelinePage — the person's own terms and wishes", () => {
  it("shows the person their own wishes and the marks their terms set", async () => {
    const [ada] = wishes();
    stubPlanningApi({ role: "access", me: 1, allocations: [allocationWithTerms()] });
    renderRoute(`/planning/people/1?to=${weekEnd(25)}`);

    const marks = await screen.findByRole("list", { name: "Earliest release and handover" });
    expect(
      within(marks)
        .getAllByRole("listitem")
        .map((item) => item.getAttribute("aria-label")),
    ).toEqual([
      `P-100 Harbour app: Earliest release ${shownDate(RELEASE)}`,
      `P-100 Harbour app: Handover from ${shownDate(HANDOVER)}`,
    ]);
    const open = screen.getByRole("list", { name: "Open wishes" });
    expect(within(open).getByRole("listitem")).toHaveTextContent(
      `Off P-100 Harbour app from ${shownDate(ada.fromDate)}Not seen yet`,
    );
  });

  it("marks a release that has no handover, and leaves the handover cell empty", async () => {
    stubPlanningApi({ allocations: [allocation({ id: 501, personId: 1, projectId: 100, earliestRelease: RELEASE })] });
    renderRoute(`/planning/people/1?to=${weekEnd(25)}`);

    const marks = await screen.findByRole("list", { name: "Earliest release and handover" });
    expect(
      within(marks)
        .getAllByRole("listitem")
        .map((item) => item.getAttribute("title")),
    ).toEqual([`P-100 Harbour app: Earliest release ${shownDate(RELEASE)}`]);
    const table = screen.getByRole("table", { name: "Allocations" });
    const cells = within(within(table).getByText("P-100 Harbour app").closest("tr") as HTMLElement).getAllByRole(
      "cell",
    );
    expect(cells[4]).toHaveTextContent(shownDate(RELEASE));
    expect(cells[5]).toHaveTextContent("—");
  });
});

describe("PersonTimeline — on Staff's person page", () => {
  it("is drawn from the person it is given, whatever the route's parameters are called", async () => {
    flagged();
    renderRoute("/staff/people/1");
    const row = (await screen.findByText("P-100 Harbour app")).closest("tr") as HTMLElement;
    expect(within(row).getByText(/^Certificate expires/)).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /Timeline/ })).not.toBeInTheDocument();
  });
});
