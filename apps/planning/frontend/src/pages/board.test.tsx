import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { addDays, isoWeek } from "../lib/weeks";
import { MONDAY, week, weekEnd } from "../test/fixtures";
import { choose, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { planningProblem, stubPlanningApi } from "../test/server";

const W = (n: number) => `W${isoWeek(week(n))}`;

/** The board's cell of a row and a week, by its accessible name. */
const cell = (name: string, n: number) => screen.findByLabelText(new RegExp(`^${name}, ${W(n)}:`));

describe("BoardPage — the board", () => {
  it("shows the people against twelve weeks, each week's load, and a tentative share hatched apart", async () => {
    const fetchMock = stubPlanningApi({ role: "view" });
    renderRoute("/planning");

    const table = await screen.findByRole("table", { name: "Plan" });
    expect(within(table).getAllByRole("columnheader")).toHaveLength(13);
    expect(within(table).getByText("Ada Lovelace")).toBeInTheDocument();
    expect(within(table).getByText("Grace Hopper")).toBeInTheDocument();

    // Ada at half time: 18.75 of 37.5 hours, the fine half of the scale.
    const ada = await cell("Ada Lovelace", 0);
    expect(ada).toHaveTextContent("50%");
    expect(ada).toHaveAccessibleName(/18\.8 h booked of 37\.5 h/);
    expect(ada.querySelector("[data-level]")).toHaveAttribute("data-level", "fine");
    // Grace is tentative only: nothing confirmed, the hatched share drawn.
    const grace = await cell("Grace Hopper", 0);
    expect(grace).toHaveTextContent("0%");
    expect(within(grace).getByTestId("tentative-share")).toBeInTheDocument();
    expect(grace).toHaveAccessibleName(/15 h tentative/);
    // A viewer may not plan: the cell is no button and there is no New allocation.
    expect(grace.tagName).not.toBe("BUTTON");
    expect(screen.queryByRole("button", { name: "New allocation" })).not.toBeInTheDocument();

    const [board] = sent(fetchMock, "GET", /\/planning\/board/);
    const query = new URL(board.url, "http://localhost").searchParams;
    expect(query.get("from")).toBe(MONDAY);
    expect(query.get("to")).toBe(weekEnd(11));
  });

  it("says a day away without red while the booking fits the days left", async () => {
    // A day away leaves 30 of 37.5 hours; Ada's 18.75 fit them.
    stubPlanningApi({ role: "view", away: { 1: [{ from: week(2), to: week(2) }] } });
    renderRoute("/planning");
    const ada = await cell("Ada Lovelace", 2);
    expect(ada.querySelector("[data-conflict]")).not.toBeInTheDocument();
    expect(ada).toHaveAccessibleName(/Away part of the week/);
  });

  it("reads red where a week with days away is booked past what is available", async () => {
    // Three days away leave 15 hours, under Ada's 18.75 (amendment 37).
    stubPlanningApi({ role: "view", away: { 1: [{ from: week(2), to: addDays(week(2), 2) }] } });
    renderRoute("/planning");
    const ada = await cell("Ada Lovelace", 2);
    expect(ada.querySelector("[data-conflict]")).toBeInTheDocument();
    expect(ada).toHaveAccessibleName(/Booked in a week with days away/);
  });

  it("shows one week when a hand-edited end lies before this week", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute(`/planning?to=${weekEnd(-3)}`);
    const table = await screen.findByRole("table", { name: "Plan" });
    expect(within(table).getAllByRole("columnheader")).toHaveLength(2);
  });

  it("keeps every department it has shown in the filter once one is picked", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning");
    const table = await screen.findByRole("table", { name: "Plan" });
    expect(await within(table).findByText("Linus Torvalds")).toBeInTheDocument();

    await choose(document.body, "Department", "Engineering");
    await waitFor(() => expect(within(table).queryByText("Linus Torvalds")).not.toBeInTheDocument());
    await userEvent.click(screen.getByRole("combobox", { name: "Department" }));
    expect(await screen.findByRole("option", { name: "Operations" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Engineering" })).toBeInTheDocument();
  });

  it("leaves out a planned project's rows when the pipeline is switched off", async () => {
    const fetchMock = stubPlanningApi({ role: "view" });
    const { router } = renderRoute("/planning?groupBy=project");

    const table = await screen.findByRole("table", { name: "Plan" });
    expect(await within(table).findByText("P-200 Tender bid")).toBeInTheDocument();
    expect(within(table).getByText("P-100 Harbour app")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("switch", { name: "Include pipeline" }));
    await waitFor(() => expect(within(table).queryByText("P-200 Tender bid")).not.toBeInTheDocument());
    expect(within(table).getByText("P-100 Harbour app")).toBeInTheDocument();
    expect(router.state.location.search).toMatchObject({ groupBy: "project", planned: false });
    const last = sent(fetchMock, "GET", /\/planning\/board/).at(-1);
    expect(last?.url).toContain("includePlanned=false");
  });

  it("filters by a competence picked from Staff's catalogue, never offering a hidden one", async () => {
    const fetchMock = stubPlanningApi({ role: "view" });
    const { router } = renderRoute("/planning");
    await screen.findByRole("table", { name: "Plan" });

    await userEvent.click(screen.getByRole("combobox", { name: "Competence" }));
    expect(await screen.findByRole("option", { name: "Safety card" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "COBOL" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("option", { name: "React" }));

    await waitFor(() => expect(router.state.location.search).toMatchObject({ competenceId: 40 }));
    await waitFor(() => expect(sent(fetchMock, "GET", /\/planning\/board/).at(-1)?.url).toContain("competenceId=40"));
  });

  it("opens a row to its allocations as bars", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning");
    await userEvent.click(await screen.findByRole("button", { name: "Show the allocations of Ada Lovelace" }));
    const lane = await screen.findByRole("list", { name: "Allocations of Ada Lovelace" });
    expect(within(lane).getByText(/P-100 Harbour app · 50% · Frontend developer/)).toBeInTheDocument();
  });

  it("explains an empty board when Staff answers the planner nobody", async () => {
    const fetchMock = stubPlanningApi({ role: "manage", meta: { workforceVisible: false } });
    renderRoute("/planning");
    expect(await screen.findByText("Staff shows you nobody")).toBeInTheDocument();
    expect(screen.getByText(/View the staff directory \(staff:view\)/)).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/planning\/board/)).toHaveLength(0);
  });
});

describe("BoardPage — the allocation sheet", () => {
  it("creates an allocation from a cell and says the warning the server answered", async () => {
    const fetchMock = stubPlanningApi({
      warnings: [{ code: "overbooked", weeks: [week(1)] }],
    });
    renderRoute("/planning");

    await userEvent.click(await cell("Ada Lovelace", 1));
    const sheet = await screen.findByRole("dialog", { name: "New allocation" });
    // The person and the week come from the cell.
    expect(within(sheet).getByRole("combobox", { name: "Person" })).toHaveValue("Ada Lovelace (E-1)");
    await choose(sheet, "Project", "P-100 Harbour app");
    await userEvent.type(within(sheet).getByRole("textbox", { name: "Percent of the position" }), "60");
    await userEvent.type(within(sheet).getByRole("textbox", { name: "Role" }), "Tech lead");
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    expect(await within(sheet).findByText("Saved — but look at this")).toBeInTheDocument();
    expect(within(sheet).getByText(`More is booked than the person's capacity in ${W(1)}.`)).toBeInTheDocument();
    const [post] = sent(fetchMock, "POST", /\/planning\/allocations$/);
    expect(post.body).toEqual({
      personId: 1,
      projectId: 100,
      fromDate: week(1),
      // Monday to Friday of the cell's week.
      toDate: addDays(week(1), 4),
      load: 60,
      loadUnit: "percent",
      status: "confirmed",
      title: "Tech lead",
    });
    expect(fetchMock.state.allocations).toHaveLength(4);
  });

  it("starts a row on a planned project as tentative", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning");
    await userEvent.click(await cell("Linus Torvalds", 0));
    const sheet = await screen.findByRole("dialog", { name: "New allocation" });
    await choose(sheet, "Project", "P-200 Tender bid");
    await userEvent.type(within(sheet).getByRole("textbox", { name: "Percent of the position" }), "20");
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/planning\/allocations$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/planning\/allocations$/)[0].body.status).toBe("tentative");
  });

  it("says an overbooking the settings refuse inline, and writes nothing", async () => {
    const fetchMock = stubPlanningApi({
      write: (method, path) =>
        method === "POST" && path === "/allocations"
          ? planningProblem(409, "overbooked", { title: "Overbooked", detail: "Weeks 2026-11-02." })
          : undefined,
    });
    renderRoute("/planning");
    await userEvent.click(await cell("Ada Lovelace", 0));
    const sheet = await screen.findByRole("dialog", { name: "New allocation" });
    await choose(sheet, "Project", "P-100 Harbour app");
    await userEvent.type(within(sheet).getByRole("textbox", { name: "Percent of the position" }), "80");
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    expect(
      await within(sheet).findByText(/This would book more than the person's capacity, and the settings refuse/),
    ).toBeInTheDocument();
    expect(fetchMock.state.allocations).toHaveLength(3);
  });

  it("puts a period that is too long on the period field", async () => {
    stubPlanningApi({
      write: () =>
        planningProblem(400, "period_too_long", {
          title: "Invalid allocation",
          errors: { toDate: ["The period is longer than 1096 days."] },
        }),
    });
    renderRoute("/planning");
    await userEvent.click(await cell("Ada Lovelace", 0));
    const sheet = await screen.findByRole("dialog", { name: "New allocation" });
    await choose(sheet, "Project", "P-100 Harbour app");
    await userEvent.type(within(sheet).getByRole("textbox", { name: "Percent of the position" }), "10");
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));
    expect(await within(sheet).findByText("At most three years (1096 days)")).toBeInTheDocument();
  });

  it("opens on arrival for the spotlight's New allocation", async () => {
    stubPlanningApi();
    const { router } = renderRoute("/planning?create=allocation");
    expect(await screen.findByRole("dialog", { name: "New allocation" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(router.state.location.search).not.toHaveProperty("create"));
  });
});
