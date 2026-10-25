import { screen, within } from "@testing-library/react";
import { describe, expect, it, onTestFinished, vi } from "vitest";
import type { StaffAbsence } from "../api/absence";
import { jsonResponse } from "../test/api";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

/** One card at a glance, by its label. */
const card = (name: string) => screen.findByRole("article", { name });

/** The cards drawn, by label, once the glance has answered. */
const cardNames = async () => {
  const glance = await screen.findByRole("group", { name: "At a glance" });
  return within(glance)
    .getAllByRole("article")
    .map((one) => one.getAttribute("aria-label"));
};

describe("At a glance — HR", () => {
  it("shows every card with Grace's figures, the contract and the care days' entitlement", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/102");

    expect(await cardNames()).toEqual([
      "Sick days",
      "Egenmelding",
      "Holiday",
      "Sick child",
      "Position",
      "Absence",
      "Contract",
      "Hours this week",
    ]);
    // Her sykmelding since 28 September: 25 working days, no egenmelding.
    const sick = await card("Sick days");
    expect(within(sick).getByText("25")).toBeInTheDocument();
    expect(within(sick).getByText("0 egenmelding · 25 sykmelding · this year")).toBeInTheDocument();
    expect(within(await card("Egenmelding")).getByText("1 of 4 times")).toBeInTheDocument();
    const holiday = await card("Holiday");
    expect(within(holiday).getByText("9 left")).toBeInTheDocument();
    expect(within(holiday).getByText("15 taken · 3 planned")).toBeInTheDocument();
    const child = await card("Sick child");
    expect(within(child).getByText("3 days")).toBeInTheDocument();
    expect(within(child).getByText("of 10 this year")).toBeInTheDocument();
    const position = await card("Position");
    expect(within(position).getByText("50 %")).toBeInTheDocument();
    expect(within(position).getByText("Operator")).toBeInTheDocument();
    expect(within(position).getByText("Since Jan 15, 2026 · 8 months")).toBeInTheDocument();
    expect(within(position).getByText("Probation ends Nov 15, 2026")).toBeInTheDocument();
    const absence = await card("Absence");
    expect(within(absence).getByText("Away: Certified sick leave Sep 28, 2026 – Oct 30, 2026")).toBeInTheDocument();
    expect(within(absence).getByText("Next: Holiday Dec 21, 2026 – Dec 23, 2026")).toBeInTheDocument();
    expect(within(absence).getByText("Sick-leave case open since Sep 28, 2026, day 14")).toBeInTheDocument();
    expect(within(await card("Contract")).getByText("Not signed")).toBeInTheDocument();
    // Grace has no user: the contract's week, and nothing logged to compare.
    const hours = await card("Hours this week");
    expect(within(hours).getByText("— of 18.75 h")).toBeInTheDocument();
    expect(within(hours).getByText("No user linked")).toBeInTheDocument();
    // A card links to the tab it expands on.
    expect(within(holiday).getByRole("link", { name: "Holiday" })).toHaveAttribute(
      "href",
      expect.stringContaining("tab=absence"),
    );
    expect(within(position).getByRole("link", { name: "Position" })).toHaveAttribute(
      "href",
      expect.stringContaining("tab=employments"),
    );
  });

  it("shows the hours a linked user logged this week against the contract", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101");

    const hours = await card("Hours this week");
    expect(within(hours).getByText("31.5 of 30 h")).toBeInTheDocument();
    expect(within(hours).getByText("20 approved")).toBeInTheDocument();
    expect(within(await card("Contract")).getByText("Signed")).toBeInTheDocument();
    expect(within(await card("Position")).getByText("Since Jan 1, 2025 · 1 y 9 m")).toBeInTheDocument();
  });

  it("says time tracking is off rather than showing a zero", async () => {
    stubStaffApi({ role: "manage", glance: { 102: { hours: { timeTracking: false } } } });
    renderRoute("/staff/people/102");

    const hours = await card("Hours this week");
    expect(within(hours).getByText("Time tracking is off")).toBeInTheDocument();
    expect(within(hours).queryByText(/ h$/)).not.toBeInTheDocument();
  });

  it("shows an overdrawn holiday account in red", async () => {
    stubStaffApi({
      role: "manage",
      glance: { 102: { holiday: { takenToDate: 20, planned: 5, remaining: -2, overdrawn: true } } },
    });
    renderRoute("/staff/people/102");

    const holiday = await card("Holiday");
    const hint = within(holiday).getByText("20 taken · 5 planned");
    expect(hint.getAttribute("style")).toMatch(/red/);
    // Overdrawn, not a negative "left".
    expect(within(holiday).getByText("2 days overdrawn")).toBeInTheDocument();
    expect(within(holiday).queryByText(/left$/)).not.toBeInTheDocument();
  });

  it("says a tenure of whole years in years alone", async () => {
    stubStaffApi({
      role: "manage",
      glance: { 102: { employment: { startDate: "2024-10-11", tenureMonths: 24, ftePercent: 50 } } },
    });
    renderRoute("/staff/people/102");

    expect(within(await card("Position")).getByText("Since Oct 11, 2024 · 2 years")).toBeInTheDocument();
  });

  it("says in one dimmed line that the overview could not be loaded", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    onTestFinished(() => {
      vi.useRealTimers();
    });
    stubStaffApi({
      role: "manage",
      read: (path) =>
        path === "/api/v1/staff/people/102/glance" ? staffProblem(500, { title: "Internal error" }) : undefined,
    });
    renderRoute("/staff/people/102");

    // The query asks again before it gives up.
    await vi.advanceTimersByTimeAsync(10_000);
    expect(await screen.findByText("Could not load the overview")).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "At a glance" })).not.toBeInTheDocument();
  });

  it("draws nothing when the glance answers 404", async () => {
    stubStaffApi({
      role: "manage",
      read: (path) =>
        path === "/api/v1/staff/people/102/glance" ? jsonResponse(404, { title: "Not found" }) : undefined,
    });
    renderRoute("/staff/people/102");

    expect(await screen.findByRole("img", { name: "Grace Hopper" })).toBeInTheDocument();
    expect(screen.queryByText("Could not load the overview")).not.toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "At a glance" })).not.toBeInTheDocument();
  });
});

describe("At a glance — staff:personal-data and the Approver", () => {
  it("shows staff:personal-data every card but the contract, with the care days' entitlement", async () => {
    stubStaffApi({ role: "personalDataOnly" });
    renderRoute("/staff/people/102");

    expect(await cardNames()).toEqual([
      "Sick days",
      "Egenmelding",
      "Holiday",
      "Sick child",
      "Position",
      "Absence",
      "Hours this week",
    ]);
    expect(within(await card("Sick child")).getByText("of 10 this year")).toBeInTheDocument();
    expect(within(await card("Hours this week")).getByText("— of 18.75 h")).toBeInTheDocument();
  });

  it("shows the Approver the absence and the position, without the hours, the contract or the entitlement", async () => {
    stubStaffApi({ role: "approve" });
    renderRoute("/staff/people/102");

    expect(await cardNames()).toEqual(["Sick days", "Egenmelding", "Holiday", "Sick child", "Position", "Absence"]);
    const child = await card("Sick child");
    expect(within(child).getByText("this year")).toBeInTheDocument();
    expect(within(child).queryByText(/of 10/)).not.toBeInTheDocument();
    expect(within(await card("Position")).getByText("Probation ends Nov 15, 2026")).toBeInTheDocument();
    expect(
      within(await card("Absence")).getByText("Away: Certified sick leave Sep 28, 2026 – Oct 30, 2026"),
    ).toBeInTheDocument();
  });
});

describe("At a glance — others", () => {
  it("shows the manager no contract and the sick-child days without the entitlement", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/102");

    expect(await cardNames()).toEqual([
      "Sick days",
      "Egenmelding",
      "Holiday",
      "Sick child",
      "Position",
      "Absence",
      "Hours this week",
    ]);
    const child = await card("Sick child");
    expect(within(child).getByText("3 days")).toBeInTheDocument();
    expect(within(child).getByText("this year")).toBeInTheDocument();
    expect(within(child).queryByText(/of 10/)).not.toBeInTheDocument();
    expect(within(await card("Position")).getByText("Probation ends Nov 15, 2026")).toBeInTheDocument();
  });

  it("shows a colleague the position and the absence as absent, and nothing else", async () => {
    stubStaffApi({ role: "view" });
    renderRoute("/staff/people/102");

    expect(await cardNames()).toEqual(["Position", "Absence"]);
    const position = await card("Position");
    expect(within(position).getByText("50 %")).toBeInTheDocument();
    expect(within(position).queryByText(/Probation/)).not.toBeInTheDocument();
    const absence = await card("Absence");
    expect(within(absence).getByText("Away: Absent Sep 28, 2026 – Oct 30, 2026")).toBeInTheDocument();
    // Neither the requested holiday nor the case is a colleague's to read.
    expect(within(absence).queryByText(/^Next:/)).not.toBeInTheDocument();
    expect(within(absence).queryByText(/Sick-leave case/)).not.toBeInTheDocument();
    // The absence tab is not theirs, so the card does not link to it.
    expect(within(absence).queryByRole("link")).not.toBeInTheDocument();
    expect(screen.queryByRole("article", { name: "Hours this week" })).not.toBeInTheDocument();
  });

  // Ferie 1–12 October approved and a sykmelding 5–16 October registered over it.
  const overlapping: StaffAbsence[] = [
    {
      id: 801,
      personId: 102,
      status: "approved",
      firstDay: "2026-10-01",
      lastDay: "2026-10-12",
      shownAs: "type",
      typeId: 1001,
    },
    {
      id: 802,
      personId: 102,
      status: "registered",
      firstDay: "2026-10-05",
      lastDay: "2026-10-16",
      shownAs: "type",
      typeId: 1003,
    },
  ];

  it("shows a colleague one span where rows overlap, with no type", async () => {
    stubStaffApi({ role: "view", absence: { people: { 102: { absences: overlapping } } } });
    renderRoute("/staff/people/102");

    const absence = await card("Absence");
    expect(within(absence).getByText("Away: Absent Oct 1, 2026 – Oct 16, 2026")).toBeInTheDocument();
    expect(within(absence).queryByText(/^Next:/)).not.toBeInTheDocument();
  });

  it("shows the manager the row of the day where rows overlap", async () => {
    stubStaffApi({ role: "access", me: 101, absence: { people: { 102: { absences: overlapping } } } });
    renderRoute("/staff/people/102");

    const absence = await card("Absence");
    expect(within(absence).getByText("Away: Holiday Oct 1, 2026 – Oct 12, 2026")).toBeInTheDocument();
  });

  it("shows the person their own glance on My page", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me");

    const hours = await card("Hours this week");
    expect(within(hours).getByText("31.5 of 30 h")).toBeInTheDocument();
    const absence = await card("Absence");
    expect(within(absence).getByText("Next: Holiday Nov 16, 2026 – Nov 20, 2026")).toBeInTheDocument();
    expect(screen.queryByRole("article", { name: "Contract" })).not.toBeInTheDocument();
  });
});
