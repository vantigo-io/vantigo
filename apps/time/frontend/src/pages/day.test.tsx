import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse, sent } from "../test/api";
import { devTaskRow, entry, kvemWorkTypes, pmRow, week, weekRow } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubTimeApi } from "../test/server";

const DAY = "2026-09-16";

const locked = { canEdit: false, canSubmit: false, canApprove: false, canUnapprove: false };

const dayWeek = () =>
  week([
    weekRow(pmRow, [
      entry({ id: 601, entryDate: DAY, hours: 2, startTime: "08:00", endTime: "10:00", note: "Status meeting" }),
      entry({ id: 699, entryDate: "2026-09-17", hours: 5, note: "Another day" }),
    ]),
    weekRow(devTaskRow, [
      entry({
        id: 602,
        ...devTaskRow,
        entryDate: DAY,
        hours: 4.5,
        status: "submitted",
        capabilities: locked,
      }),
    ]),
  ]);

/** Picks an option from a Mantine select inside the dialog. */
const choose = async (dialog: HTMLElement, label: string, option: RegExp | string) => {
  await userEvent.click(within(dialog).getByRole("combobox", { name: label }));
  await userEvent.click(await screen.findByRole("option", { name: option }));
};

/** A native time input takes its value whole, the way a browser's time picker sets it. */
const setTime = (dialog: HTMLElement, label: string, value: string) =>
  fireEvent.change(within(dialog).getByLabelText(label), { target: { value } });

describe("DayPage", () => {
  it("offers the project's active work types, forgets the choice when the project changes, and sends it", async () => {
    const fetchMock = stubTimeApi({ week: dayWeek(), workTypes: { 1001: kvemWorkTypes } });
    renderRoute(`/time/day?date=${DAY}`);

    await screen.findByText("Status meeting");
    await userEvent.click(screen.getByRole("button", { name: "Add entry" }));
    const dialog = await screen.findByRole("dialog", { name: "Log time" });

    // No project, no choice; a project without work types, none either.
    expect(within(dialog).queryByRole("combobox", { name: "Work type" })).not.toBeInTheDocument();
    await choose(dialog, "Project", /INTERN/);
    expect(within(dialog).queryByRole("combobox", { name: "Work type" })).not.toBeInTheDocument();

    await choose(dialog, "Project", /KVEM1000/);
    await userEvent.click(await within(dialog).findByRole("combobox", { name: "Work type" }));
    expect(await screen.findByRole("option", { name: "Overtid 50 %" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Gammel overtid" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("option", { name: "Overtid 50 %" }));
    expect(within(dialog).getByRole("combobox", { name: "Work type" })).toHaveValue("Overtid 50 %");

    await choose(dialog, "Project", /INTERN/);
    await choose(dialog, "Project", /KVEM1000/);
    const select = await within(dialog).findByRole("combobox", { name: "Work type" });
    expect(select).toHaveValue("");
    expect(select).toHaveAttribute("placeholder", "Ordinary hours");

    await choose(dialog, "Work type", "Overtid 50 %");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Hours" }), "2");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST").body).toMatchObject({ projectId: 1001, workTypeId: 6001 }));
  });

  it("shows an entry's work type after its trackable code, its multiplied rate, and keeps the type on an edit", async () => {
    const overtime = entry({
      id: 610,
      entryDate: DAY,
      hours: 2,
      note: "Late deploy",
      workType: { id: 6001, name: "Overtid 50 %" },
      billing: { billRate: 900, currency: "NOK", multiplierPercent: 150, effectiveRate: 1350 },
    });
    const fetchMock = stubTimeApi({ week: week([weekRow(pmRow, [overtime])]), workTypes: { 1001: kvemWorkTypes } });
    renderRoute(`/time/day?date=${DAY}`);

    const card = (await screen.findByText("Late deploy")).closest("[data-entry]") as HTMLElement;
    expect(within(card).getByTestId("work-type-badge")).toHaveTextContent("Overtid 50 %");
    expect(within(card).getByTestId("rate-line")).toHaveTextContent("900 × 150 % = 1,350");

    await userEvent.click(within(card).getByRole("button", { name: "Edit the entry" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit time" });
    expect(await within(dialog).findByRole("combobox", { name: "Work type" })).toHaveValue("Overtid 50 %");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").body).toMatchObject({ workTypeId: 6001, revision: 2 }));
  });

  // A type deactivated since the entry picked it is no choice for new work,
  // but the entry still names it: the form shows it as the current value, and
  // the server's refusal to keep it lands on the field (D3).
  it("keeps a retired work type as an edited entry's value, and puts the refusal on the field", async () => {
    const retired = entry({
      id: 611,
      entryDate: DAY,
      hours: 1,
      note: "Old overtime",
      billable: false,
      workType: { id: 6003, name: "Gammel overtid" },
      // Not billable: the multiplier is snapshotted, but nothing bills at it.
      billing: { billRate: 900, currency: "NOK", multiplierPercent: 150 },
    });
    stubTimeApi({
      week: week([weekRow(pmRow, [retired])]),
      workTypes: { 1001: kvemWorkTypes },
      write: (method) =>
        method === "PUT"
          ? problemResponse(400, "Invalid time entry", { workTypeId: ["Work type is no longer active"] })
          : undefined,
    });
    renderRoute(`/time/day?date=${DAY}`);

    const card = (await screen.findByText("Old overtime")).closest("[data-entry]") as HTMLElement;
    expect(within(card).getByTestId("work-type-badge")).toHaveTextContent("Gammel overtid");
    expect(within(card).queryByTestId("rate-line")).not.toBeInTheDocument();

    await userEvent.click(within(card).getByRole("button", { name: "Edit the entry" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit time" });
    expect(await within(dialog).findByRole("combobox", { name: "Work type" })).toHaveValue("Gammel overtid");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText("Work type is no longer active")).toBeInTheDocument();
  });

  it("lists the day's own entries with their times, notes, hours and status, and the day total", async () => {
    stubTimeApi({ week: dayWeek() });
    renderRoute(`/time/day?date=${DAY}`);

    const meeting = (await screen.findByText("Status meeting")).closest("[data-entry]") as HTMLElement;
    expect(meeting).toHaveTextContent("KVEM1000 › PM");
    expect(meeting).toHaveTextContent("08:00–10:00");
    expect(meeting).toHaveTextContent("2 h");
    expect(meeting).toHaveTextContent("Draft");

    const task = screen.getByText("KVEM1000 › DEV › Skriv spesifikasjonen").closest("[data-entry]") as HTMLElement;
    expect(task).toHaveTextContent("Submitted");
    expect(within(task).queryByRole("button", { name: "Edit the entry" })).not.toBeInTheDocument();
    expect(within(task).queryByRole("button", { name: "Delete the entry" })).not.toBeInTheDocument();

    expect(screen.queryByText("Another day")).not.toBeInTheDocument();
    expect(screen.getByTestId("day-total")).toHaveTextContent("6.5 h");
  });

  it("works the hours out from a start and end time and logs them", async () => {
    const fetchMock = stubTimeApi({ week: dayWeek() });
    renderRoute(`/time/day?date=${DAY}`);

    await screen.findByText("Status meeting");
    await userEvent.click(screen.getByRole("button", { name: "Add entry" }));
    const dialog = await screen.findByRole("dialog", { name: "Log time" });

    await choose(dialog, "Project", /KVEM1000/);
    await choose(dialog, "Line", /PM/);
    setTime(dialog, "Start", "12:00");
    setTime(dialog, "End", "15:30");

    const hours = within(dialog).getByRole("textbox", { name: "Hours" });
    expect(hours).toHaveValue("3.5");
    expect(hours).toBeDisabled();
    expect(within(dialog).getByText("Worked out from the start and end time.")).toBeInTheDocument();

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note" }), "Workshop");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({
        url: "/api/v1/time/entries",
        body: {
          projectId: 1001,
          billingLineId: 3001,
          taskId: null,
          entryDate: DAY,
          hours: 3.5,
          startTime: "12:00",
          endTime: "15:30",
          note: "Workshop",
          billable: true,
        },
      }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("takes hours typed in any of the three forms when no times are given", async () => {
    const fetchMock = stubTimeApi({ week: dayWeek() });
    renderRoute(`/time/day?date=${DAY}`);

    await screen.findByText("Status meeting");
    await userEvent.click(screen.getByRole("button", { name: "Add entry" }));
    const dialog = await screen.findByRole("dialog", { name: "Log time" });
    await choose(dialog, "Project", /KVEM1000/);
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Hours" }), "1:15");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST").body).toMatchObject({
        hours: 1.25,
        startTime: null,
        endTime: null,
        billingLineId: null,
      }),
    );
  });

  it("refuses a start without an end", async () => {
    const fetchMock = stubTimeApi({ week: dayWeek() });
    renderRoute(`/time/day?date=${DAY}`);

    await screen.findByText("Status meeting");
    await userEvent.click(screen.getByRole("button", { name: "Add entry" }));
    const dialog = await screen.findByRole("dialog", { name: "Log time" });
    await choose(dialog, "Project", /KVEM1000/);
    setTime(dialog, "Start", "12:00");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Hours" }), "2");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText("Give both a start and an end time, or neither")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  it("hides the billable switch on a non-billable project and logs the time as not billable", async () => {
    const fetchMock = stubTimeApi({ week: dayWeek() });
    renderRoute(`/time/day?date=${DAY}`);

    await screen.findByText("Status meeting");
    await userEvent.click(screen.getByRole("button", { name: "Add entry" }));
    const dialog = await screen.findByRole("dialog", { name: "Log time" });
    expect(within(dialog).queryByRole("switch", { name: "Billable" })).not.toBeInTheDocument();
    await choose(dialog, "Project", /KVEM1000/);
    expect(within(dialog).getByRole("switch", { name: "Billable" })).toBeChecked();
    await choose(dialog, "Project", /INTERN/);
    expect(within(dialog).queryByRole("switch", { name: "Billable" })).not.toBeInTheDocument();

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Hours" }), "1");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST").body).toMatchObject({ projectId: 1002, billable: false }));
  });

  it("edits a draft, carrying its revision", async () => {
    const fetchMock = stubTimeApi({ week: dayWeek() });
    renderRoute(`/time/day?date=${DAY}`);

    const meeting = (await screen.findByText("Status meeting")).closest("[data-entry]") as HTMLElement;
    await userEvent.click(within(meeting).getByRole("button", { name: "Edit the entry" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit time" });
    expect(within(dialog).getByRole("textbox", { name: "Hours" })).toHaveValue("2");
    setTime(dialog, "End", "11:00");
    expect(within(dialog).getByRole("textbox", { name: "Hours" })).toHaveValue("3");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const { url, body } = sent(fetchMock, "PUT");
      expect(url).toBe("/api/v1/time/entries/601");
      expect(body).toEqual({
        projectId: 1001,
        billingLineId: 3001,
        taskId: null,
        entryDate: DAY,
        hours: 3,
        startTime: "08:00",
        endTime: "11:00",
        note: "Status meeting",
        billable: true,
        revision: 2,
      });
    });
  });

  it("deletes a draft once the caller confirms", async () => {
    const fetchMock = stubTimeApi({ week: dayWeek() });
    renderRoute(`/time/day?date=${DAY}`);

    const meeting = (await screen.findByText("Status meeting")).closest("[data-entry]") as HTMLElement;
    await userEvent.click(within(meeting).getByRole("button", { name: "Delete the entry" }));
    const confirm = await screen.findByRole("dialog", { name: "Delete the entry?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Delete" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "DELETE") ?? [];
      expect(String(url)).toBe("/api/v1/time/entries/601");
      expect(init?.method).toBe("DELETE");
    });
    expect(await screen.findByText("Entry deleted")).toBeInTheDocument();
  });

  it("offers no changes on a locked day", async () => {
    stubTimeApi({ week: dayWeek(), settings: { lockedBefore: "2026-09-17" } });
    renderRoute(`/time/day?date=${DAY}`);

    await screen.findByText("Status meeting");
    expect(await screen.findByText("This day is locked. Its entries can no longer be changed.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add entry" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit the entry" })).not.toBeInTheDocument();
  });

  it("withdraws an approval from the entry that carries the capability", async () => {
    const approved = week([
      weekRow(pmRow, [
        entry({
          id: 603,
          entryDate: DAY,
          hours: 3,
          note: "Approved already",
          status: "approved",
          capabilities: { canEdit: false, canSubmit: false, canApprove: false, canUnapprove: true },
        }),
      ]),
    ]);
    const fetchMock = stubTimeApi({ week: approved });
    renderRoute(`/time/day?date=${DAY}`);

    const card = (await screen.findByText("Approved already")).closest("[data-entry]") as HTMLElement;
    await userEvent.click(within(card).getByRole("button", { name: "Withdraw the approval" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/time/entries/unapprove", body: { ids: [603] } }),
    );
    expect(await screen.findByText("Approval withdrawn")).toBeInTheDocument();
  });

  it("moves between days through the URL", async () => {
    stubTimeApi({ week: dayWeek() });
    const { router } = renderRoute(`/time/day?date=${DAY}`);

    await screen.findByText("Status meeting");
    await userEvent.click(screen.getByRole("button", { name: "Next day" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ date: "2026-09-17" }));
    expect(await screen.findByText("Another day")).toBeInTheDocument();
  });
});
