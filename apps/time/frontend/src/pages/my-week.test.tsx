import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { jsonResponse, sent } from "../test/api";
import { devTaskRow, entry, pmRow, WEEK, week, weekRow } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubTimeApi } from "../test/server";

const quote = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** The hours input of a row on a weekday, named the way a screen reader hears it. */
const cell = (row: string, weekday: string) =>
  screen.getByRole("textbox", { name: new RegExp(`^${quote(row)} on ${weekday}\\b`) });

const findCell = async (row: string, weekday: string) => {
  await screen.findByRole("textbox", { name: new RegExp(`^${quote(row)} on ${weekday}\\b`) });
  return cell(row, weekday);
};

const PM = "KVEM1000 › PM";
const DEV_TASK = "KVEM1000 › DEV › Skriv spesifikasjonen";

const typicalWeek = () =>
  week([
    weekRow(pmRow, [
      entry({ id: 501, entryDate: WEEK, hours: 7.5 }),
      entry({
        id: 502,
        entryDate: "2026-09-15",
        hours: 4,
        status: "submitted",
        capabilities: { canEdit: false, canSubmit: false, canApprove: false, canUnapprove: false },
      }),
    ]),
    weekRow(devTaskRow, [
      entry({
        id: 503,
        ...devTaskRow,
        entryDate: "2026-09-16",
        hours: 2,
        status: "approved",
        capabilities: { canEdit: false, canSubmit: false, canApprove: false, canUnapprove: false },
      }),
    ]),
  ]);

describe("MyWeekPage", () => {
  afterEach(() => vi.useRealTimers());

  it("shows a row per trackable with its hours per day, the day totals and the week total", async () => {
    stubTimeApi({ week: typicalWeek() });
    renderRoute(`/time?week=${WEEK}`);

    expect(await findCell(PM, "Monday")).toHaveValue("7.5");
    expect(cell(PM, "Tuesday")).toHaveValue("4");
    expect(cell(PM, "Wednesday")).toHaveValue("");
    expect(cell(DEV_TASK, "Wednesday")).toHaveValue("2");
    expect(screen.getAllByText("Kverneland web")).toHaveLength(2);

    const totals = screen.getByTestId("week-totals");
    const values = within(totals)
      .getAllByRole("cell")
      .map((c) => c.textContent);
    expect(values).toEqual(["Total", "7.5", "4", "2", "0", "0", "0", "0", "13.5"]);
  });

  it("opens on the caller's current week when the URL names none", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 8, 20, 12, 0)); // a Sunday
    const fetchMock = stubTimeApi();
    renderRoute("/time");

    await screen.findByText("Nothing logged this week");
    expect(fetchMock.actualCalls.map(([url]) => String(url))).toContain("/api/v1/time/weeks/2026-09-14");
  });

  it("moves between weeks through the URL", async () => {
    stubTimeApi({ week: typicalWeek() });
    const { router } = renderRoute(`/time?week=${WEEK}`);

    await findCell(PM, "Monday");
    await userEvent.click(screen.getByRole("button", { name: "Previous week" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ week: "2026-09-07" }));
    await userEvent.click(screen.getByRole("button", { name: "Next week" }));
    await userEvent.click(screen.getByRole("button", { name: "Next week" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ week: "2026-09-21" }));
  });

  it("creates an entry for the row's project, line and task when hours are typed into an empty day", async () => {
    const fetchMock = stubTimeApi({ week: typicalWeek() });
    renderRoute(`/time?week=${WEEK}`);

    await userEvent.type(await findCell(DEV_TASK, "Tuesday"), "7:30{Enter}");

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({
        url: "/api/v1/time/entries",
        body: { projectId: 1001, billingLineId: 3002, taskId: 5001, entryDate: "2026-09-15", hours: 7.5 },
      }),
    );
    expect(fetchMock.actualCalls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
  });

  it("replaces a draft with the new hours and the revision it was read at", async () => {
    const fetchMock = stubTimeApi({ week: typicalWeek() });
    renderRoute(`/time?week=${WEEK}`);

    const monday = await findCell(PM, "Monday");
    await userEvent.clear(monday);
    await userEvent.type(monday, "6,5");
    await userEvent.tab();

    await waitFor(() => {
      const { url, body } = sent(fetchMock, "PUT");
      expect(url).toBe("/api/v1/time/entries/501");
      expect(body).toMatchObject({
        projectId: 1001,
        billingLineId: 3001,
        entryDate: WEEK,
        hours: 6.5,
        revision: 2,
        billable: true,
      });
    });
  });

  it("deletes a draft whose hours are cleared", async () => {
    const fetchMock = stubTimeApi({ week: typicalWeek() });
    renderRoute(`/time?week=${WEEK}`);

    await userEvent.clear(await findCell(PM, "Monday"));
    await userEvent.tab();

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "DELETE") ?? [];
      expect(String(url)).toBe("/api/v1/time/entries/501");
      expect(init?.method).toBe("DELETE");
    });
  });

  it("cancels an edit on Escape without saving anything", async () => {
    const fetchMock = stubTimeApi({ week: typicalWeek() });
    renderRoute(`/time?week=${WEEK}`);

    const monday = await findCell(PM, "Monday");
    await userEvent.clear(monday);
    await userEvent.type(monday, "3{Escape}");
    await userEvent.tab();

    expect(cell(PM, "Monday")).toHaveValue("7.5");
    expect(fetchMock.actualCalls.some(([, init]) => (init?.method ?? "GET") !== "GET")).toBe(false);
  });

  it("keeps a day out of the grid while its entry has a start and an end time", async () => {
    const fetchMock = stubTimeApi({
      week: week([
        weekRow(pmRow, [entry({ id: 530, entryDate: "2026-09-18", hours: 2, startTime: "08:00", endTime: "10:00" })]),
      ]),
    });
    const { router } = renderRoute(`/time?week=${WEEK}`);

    const timed = await findCell(PM, "Friday");
    expect(timed).toHaveValue("2");
    expect(timed).toHaveAttribute("readonly");

    await userEvent.hover(timed);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("08:00–10:00");

    await userEvent.click(timed);
    await waitFor(() => expect(router.state.location.pathname).toBe("/time/day"));
    expect(router.state.location.search).toEqual({ date: "2026-09-18" });
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "PUT")).toBe(false);
  });

  it("keeps a row whose only entry was cleared, ready to be typed into again", async () => {
    let current = week([weekRow(pmRow, [entry({ id: 540, entryDate: WEEK, hours: 7.5 })])]);
    const fetchMock = stubTimeApi({
      week: () => current,
      write: (method) => {
        if (method === "DELETE") current = week([]);
        return undefined;
      },
    });
    renderRoute(`/time?week=${WEEK}`);

    await userEvent.clear(await findCell(PM, "Monday"));
    await userEvent.tab();

    await waitFor(() => expect(cell(PM, "Monday")).toHaveValue(""));
    await userEvent.type(cell(PM, "Tuesday"), "4{Enter}");

    await waitFor(() =>
      expect(sent(fetchMock, "POST").body).toEqual({
        projectId: 1001,
        billingLineId: 3001,
        entryDate: "2026-09-15",
        hours: 4,
      }),
    );
  });

  it("keeps submitted and approved hours read-only and says why on hover", async () => {
    stubTimeApi({ week: typicalWeek() });
    renderRoute(`/time?week=${WEEK}`);

    const submitted = await findCell(PM, "Tuesday");
    expect(submitted).toHaveAttribute("readonly");
    expect(submitted).toHaveAttribute("data-status", "submitted");
    expect(cell(DEV_TASK, "Wednesday")).toHaveAttribute("readonly");
    expect(cell(PM, "Monday")).not.toHaveAttribute("readonly");

    await userEvent.hover(submitted);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Submitted");
  });

  it("lets a rejected entry be corrected and shows the reason on hover", async () => {
    stubTimeApi({
      week: week([
        weekRow(pmRow, [
          entry({ id: 510, entryDate: "2026-09-17", hours: 3, status: "rejected", rejectionReason: "Wrong line" }),
        ]),
      ]),
    });
    renderRoute(`/time?week=${WEEK}`);

    const rejected = await findCell(PM, "Thursday");
    expect(rejected).not.toHaveAttribute("readonly");
    expect(rejected).toHaveAttribute("data-status", "rejected");
    await userEvent.hover(rejected);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("Rejected: Wrong line");
  });

  it("keeps the days before the lock date read-only, empty ones included", async () => {
    stubTimeApi({ week: typicalWeek(), settings: { lockedBefore: "2026-09-16" } });
    renderRoute(`/time?week=${WEEK}`);

    await findCell(PM, "Monday");
    await waitFor(() => expect(cell(PM, "Monday")).toHaveAttribute("readonly"));
    expect(cell(DEV_TASK, "Tuesday")).toHaveAttribute("readonly");
    expect(cell(DEV_TASK, "Thursday")).not.toHaveAttribute("readonly");
    expect(screen.getByText(/Days before .* are locked/)).toBeInTheDocument();
  });

  it("shows the server's message when the day would hold too many hours", async () => {
    const fetchMock = stubTimeApi({
      week: typicalWeek(),
      write: (method) =>
        method === "POST"
          ? jsonResponse(400, {
              title: "Invalid time entry",
              errors: { hours: ["Your hours on 2026-09-17 would total 25, and a day holds at most 24"] },
            })
          : undefined,
    });
    renderRoute(`/time?week=${WEEK}`);

    await userEvent.type(await findCell(PM, "Thursday"), "25{Enter}");
    expect(await screen.findByText("Not a duration")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);

    await userEvent.type(cell(PM, "Thursday"), "20{Enter}");
    expect(
      await screen.findByText("Your hours on 2026-09-17 would total 25, and a day holds at most 24"),
    ).toBeInTheDocument();
    await waitFor(() => expect(cell(PM, "Thursday")).toHaveValue(""));
  });

  it("adds a row through the picker — project, line, task — and logs hours on it", async () => {
    const fetchMock = stubTimeApi({ week: week([weekRow(pmRow, [entry()])]) });
    renderRoute(`/time?week=${WEEK}`);

    await findCell(PM, "Monday");
    await userEvent.click(screen.getByRole("button", { name: "Add row" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a row" });
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Project" }));
    await userEvent.click(await screen.findByRole("option", { name: /KVEM1000/ }));
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Line" }));
    await userEvent.click(await screen.findByRole("option", { name: /DEV/ }));
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Task" }));
    await userEvent.click(await screen.findByRole("option", { name: "Skriv spesifikasjonen" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await userEvent.type(cell(DEV_TASK, "Wednesday"), "2{Enter}");

    await waitFor(() =>
      expect(sent(fetchMock, "POST").body).toEqual({
        projectId: 1001,
        billingLineId: 3002,
        taskId: 5001,
        entryDate: "2026-09-16",
        hours: 2,
      }),
    );
  });

  it("adds a row for each open task on a project the caller logs time on", async () => {
    stubTimeApi({ week: week([]) });
    renderRoute(`/time?week=${WEEK}`);

    await screen.findByText("Nothing logged this week");
    await userEvent.click(screen.getByRole("button", { name: "From my tasks" }));

    expect(await findCell("KVEM1000 › Skriv spesifikasjonen", "Monday")).toHaveValue("");
    expect(screen.queryByText(/EURO2026/)).not.toBeInTheDocument();
  });

  it("submits the week once the caller confirms", async () => {
    const fetchMock = stubTimeApi({ week: typicalWeek() });
    renderRoute(`/time?week=${WEEK}`);

    await findCell(PM, "Monday");
    await userEvent.click(screen.getByRole("button", { name: "Submit week" }));
    const confirm = await screen.findByRole("dialog", { name: "Submit the week?" });
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
    await userEvent.click(within(confirm).getByRole("button", { name: "Submit" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe(`/api/v1/time/weeks/${WEEK}/submit`));
    expect(await screen.findByText("Week submitted")).toBeInTheDocument();
  });

  it("warns when drafts appeared after the week was submitted", async () => {
    stubTimeApi({
      week: { ...typicalWeek(), submittedAt: "2026-09-18T15:00:00Z", hasUnsubmittedChanges: true },
    });
    renderRoute(`/time?week=${WEEK}`);

    expect(await screen.findByText("Changed since you submitted it")).toBeInTheDocument();
  });

  it("sends a day holding several entries of a row to the day view", async () => {
    stubTimeApi({
      week: week([
        weekRow(pmRow, [
          entry({ id: 520, entryDate: "2026-09-18", hours: 2, startTime: "08:00", endTime: "10:00" }),
          entry({ id: 521, entryDate: "2026-09-18", hours: 3, startTime: "12:00", endTime: "15:00" }),
        ]),
      ]),
    });
    renderRoute(`/time?week=${WEEK}`);

    const friday = await findCell(PM, "Friday");
    expect(friday).toHaveValue("5");
    expect(friday).toHaveAttribute("readonly");
  });
});
