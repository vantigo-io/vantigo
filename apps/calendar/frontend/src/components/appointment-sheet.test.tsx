import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setTimeZones } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import { problemResponse, sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import { appointmentFixture, KARI, ME, OLA, stubCalendarServer } from "../test/server";
import { AppointmentSheet } from "./appointment-sheet";

// The session's zone is Oslo, the machine's New York (vite.config.ts): a
// sheet that read typed times in the machine's zone would send instants six
// hours off. November: Oslo is UTC+1.
beforeEach(() =>
  setTimeZones({ timeZone: "Europe/Oslo", timeZoneSource: "personal", businessTimeZone: "Europe/Oslo" }),
);
afterEach(() => setTimeZones(null));

const save = () => userEvent.click(screen.getByRole("button", { name: "Save" }));

describe("AppointmentSheet — a new appointment", () => {
  it("creates an all-day appointment with the session's zone", async () => {
    const fetchMock = stubCalendarServer();
    const onClose = vi.fn();
    renderWithProviders(
      <AppointmentSheet
        opened
        onClose={onClose}
        span={{ allDay: true, startDate: "2026-11-03", endDate: "2026-11-04" }}
      />,
    );
    await userEvent.type(await screen.findByLabelText(/^Title/), "Trade fair");
    await save();

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const { url, body } = sent(fetchMock, "POST");
    expect(url).toBe("/api/v1/calendar/appointments");
    expect(body).toEqual({
      title: "Trade fair",
      allDay: true,
      startDate: "2026-11-03",
      endDate: "2026-11-04",
      timeZone: "Europe/Oslo",
      visibility: "shared",
      transparency: "busy",
      attendees: [],
    });
  });

  it("creates a timed appointment from the wall-clock pair, as instants in the session's zone", async () => {
    const fetchMock = stubCalendarServer();
    renderWithProviders(
      <AppointmentSheet
        opened
        onClose={() => undefined}
        span={{ allDay: false, startsAt: "2026-11-03T08:00:00.000Z", endsAt: "2026-11-03T09:00:00.000Z" }}
      />,
    );
    // Shown back in Oslo: 09:00–10:00.
    const start = await screen.findByLabelText(/^Start time/);
    expect(start).toHaveValue("09:00");
    expect(screen.getByLabelText(/^End time/)).toHaveValue("10:00");
    expect(screen.getByText("Times are in Europe/Oslo.")).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText(/^Title/), "Kickoff");
    fireEvent.change(start, { target: { value: "10:15" } });
    fireEvent.change(screen.getByLabelText(/^End time/), { target: { value: "11:00" } });
    await userEvent.type(screen.getByLabelText("Location"), "Bergen");
    await userEvent.click(screen.getByText("Private"));
    await save();

    await waitFor(() => expect(sent(fetchMock, "POST").body).toBeDefined());
    expect(sent(fetchMock, "POST").body).toEqual({
      title: "Kickoff",
      allDay: false,
      startsAt: "2026-11-03T09:15:00.000Z",
      endsAt: "2026-11-03T10:00:00.000Z",
      timeZone: "Europe/Oslo",
      location: "Bergen",
      visibility: "private",
      transparency: "busy",
      attendees: [],
    });
  });

  it("sends the rule the recurrence control makes", async () => {
    const fetchMock = stubCalendarServer();
    renderWithProviders(
      <AppointmentSheet
        opened
        onClose={() => undefined}
        span={{ allDay: false, startsAt: "2026-11-03T08:00:00.000Z", endsAt: "2026-11-03T09:00:00.000Z" }}
      />,
    );
    await userEvent.type(await screen.findByLabelText(/^Title/), "Standup");
    await userEvent.click(screen.getByRole("combobox", { name: "Repeat" }));
    await userEvent.click(await screen.findByRole("option", { name: "Weekly" }));
    await userEvent.click(screen.getByText("Thu"));
    await userEvent.click(screen.getByText("After a number"));
    await save();

    await waitFor(() => expect(sent(fetchMock, "POST").body).toBeDefined());
    expect(sent(fetchMock, "POST").body.recurrence).toEqual({ rule: "FREQ=WEEKLY;INTERVAL=1;BYDAY=TU,TH;COUNT=10" });
  });

  it("carries the customer it was made from, and picks attendees without the owner", async () => {
    const fetchMock = stubCalendarServer({ users: [{ id: ME, displayName: "Test User" }, KARI, OLA] });
    renderWithProviders(
      <AppointmentSheet
        opened
        onClose={() => undefined}
        customerId={42}
        span={{ allDay: true, startDate: "2026-11-03", endDate: "2026-11-03" }}
      />,
    );
    expect(await screen.findByText("Customer: no. 42")).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText(/^Title/), "Visit");
    await userEvent.click(screen.getByRole("combobox", { name: "Attendees" }));
    await userEvent.click(await screen.findByRole("option", { name: "Kari Nordmann" }));
    expect(screen.queryByRole("option", { name: "Test User" })).not.toBeInTheDocument();
    await save();

    await waitFor(() => expect(sent(fetchMock, "POST").body).toBeDefined());
    expect(sent(fetchMock, "POST").body).toMatchObject({ customerId: 42, attendees: [KARI.id] });
  });

  it("puts the server's field errors on the fields they name", async () => {
    stubCalendarServer({
      refuse: { POST: problemResponse(400, "Invalid", { errors: { title: ["The title is too long"] } }) },
    });
    renderWithProviders(
      <AppointmentSheet
        opened
        onClose={() => undefined}
        span={{ allDay: true, startDate: "2026-11-03", endDate: "2026-11-03" }}
      />,
    );
    await userEvent.type(await screen.findByLabelText(/^Title/), "Long");
    await save();
    expect(await screen.findByText("The title is too long")).toBeInTheDocument();
  });
});

describe("AppointmentSheet — an occurrence of a series", () => {
  // Weekly on Tuesdays 08:00–09:00 Oslo since 6 October (summer time: 06:00Z).
  const series = appointmentFixture({
    startsAt: "2026-10-06T06:00:00Z",
    endsAt: "2026-10-06T07:00:00Z",
    recurrence: { rule: "FREQ=WEEKLY;INTERVAL=1;BYDAY=TU" },
  });

  const openOccurrence = async () => {
    const fetchMock = stubCalendarServer({ appointments: [series] });
    renderWithProviders(
      <AppointmentSheet opened onClose={() => undefined} appointmentId={1001} occurrenceDate="2026-11-03" />,
    );
    // The occurrence on 3 November is at 08:00 Oslo, which is now winter time.
    const start = await screen.findByLabelText(/^Start time/);
    expect(start).toHaveValue("08:00");
    fireEvent.change(start, { target: { value: "09:00" } });
    fireEvent.change(screen.getByLabelText(/^End time/), { target: { value: "10:00" } });
    await save();
    return { fetchMock, dialog: await screen.findByRole("dialog", { name: "Change this occurrence or the series?" }) };
  };

  it("moves this occurrence alone with the occurrence PUT", async () => {
    const { fetchMock, dialog } = await openOccurrence();
    await userEvent.click(within(dialog).getByRole("button", { name: "This occurrence" }));

    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(sent(fetchMock, "PUT")).toMatchObject({
      url: "/api/v1/calendar/appointments/1001/occurrences/2026-11-03",
      body: { startsAt: "2026-11-03T08:00:00.000Z", endsAt: "2026-11-03T09:00:00.000Z" },
    });
  });

  it("moves the series by the same hour with the appointment PUT at its revision", async () => {
    const { fetchMock, dialog } = await openOccurrence();
    expect(within(dialog).getByText(/removes the changes made to single occurrences/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "The series" }));

    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    const put = sent(fetchMock, "PUT");
    expect(put.url).toBe("/api/v1/calendar/appointments/1001");
    expect(put.headers["if-match"]).toBe("3");
    expect(put.body).toMatchObject({
      startsAt: "2026-10-06T07:00:00.000Z",
      endsAt: "2026-10-06T08:00:00.000Z",
      timeZone: "Europe/Oslo",
      recurrence: { rule: "FREQ=WEEKLY;INTERVAL=1;BYDAY=TU" },
    });
  });

  it("cancels this occurrence alone when deleting it", async () => {
    const fetchMock = stubCalendarServer({ appointments: [series] });
    renderWithProviders(
      <AppointmentSheet opened onClose={() => undefined} appointmentId={1001} occurrenceDate="2026-11-03" />,
    );
    await userEvent.click(await screen.findByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete this occurrence or the series?" });
    await userEvent.click(within(dialog).getByRole("button", { name: "This occurrence" }));

    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(sent(fetchMock, "PUT")).toMatchObject({
      url: "/api/v1/calendar/appointments/1001/occurrences/2026-11-03",
      body: { cancelled: true },
    });
    expect(sentAll(fetchMock, "DELETE")).toHaveLength(0);
  });

  it("says so when the appointment moved on since it was read", async () => {
    stubCalendarServer({
      appointments: [appointmentFixture()],
      refuse: { PUT: problemResponse(409, "Revision mismatch", { code: "revision_mismatch" }) },
    });
    renderWithProviders(<AppointmentSheet opened onClose={() => undefined} appointmentId={1001} />);
    await screen.findByDisplayValue("Kickoff with Nordlys AS");
    await save();
    expect(await screen.findByText("Someone changed this appointment")).toBeInTheDocument();
  });
});

describe("AppointmentSheet — the appointment's own zone", () => {
  it("keeps the zone the appointment was made in when its title is changed from another zone", async () => {
    // Made in Oslo; the owner's personal zone has since become Tokyo.
    setTimeZones({ timeZone: "Asia/Tokyo", timeZoneSource: "personal", businessTimeZone: "Europe/Oslo" });
    const fetchMock = stubCalendarServer({ appointments: [appointmentFixture({ timeZone: "Europe/Oslo" })] });
    renderWithProviders(<AppointmentSheet opened onClose={() => undefined} appointmentId={1001} />);

    const title = await screen.findByDisplayValue("Kickoff with Nordlys AS");
    // Shown in the viewer's zone: 07:00Z is 16:00 in Tokyo.
    expect(screen.getByLabelText(/^Start time/)).toHaveValue("16:00");
    await userEvent.clear(title);
    await userEvent.type(title, "Kickoff, moved room");
    await save();

    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(sent(fetchMock, "PUT").body).toMatchObject({
      title: "Kickoff, moved room",
      timeZone: "Europe/Oslo",
      startsAt: "2026-10-20T07:00:00.000Z",
      endsAt: "2026-10-20T08:00:00.000Z",
    });
  });
});

describe("AppointmentSheet — a series' exceptions", () => {
  const series = appointmentFixture({
    startsAt: "2026-10-06T06:00:00Z",
    endsAt: "2026-10-06T07:00:00Z",
    recurrence: { rule: "FREQ=WEEKLY;INTERVAL=1;BYDAY=TU" },
    exceptions: [
      {
        occurrenceDate: "2026-11-03",
        cancelled: false,
        startsAt: "2026-11-04T09:00:00Z",
        endsAt: "2026-11-04T10:00:00Z",
      },
      { occurrenceDate: "2026-12-22", cancelled: true },
    ],
  });

  it("lists the cancelled and moved occurrences, and puts one back", async () => {
    const fetchMock = stubCalendarServer({ appointments: [series] });
    renderWithProviders(<AppointmentSheet opened onClose={() => undefined} appointmentId={1001} />);

    // In Oslo, winter time: 09:00Z is 10:00.
    expect(await screen.findByText("Moved from Nov 3, 2026 to Nov 4, 2026 10:00")).toBeInTheDocument();
    const cancelled = screen.getByText("Cancelled Dec 22, 2026");
    const row = cancelled.closest("[data-testid='occurrence-exception']") as HTMLElement;
    await userEvent.click(within(row).getByRole("button", { name: "Put back" }));

    await waitFor(() => expect(sentAll(fetchMock, "DELETE")).toHaveLength(1));
    expect(sent(fetchMock, "DELETE").url).toBe("/api/v1/calendar/appointments/1001/occurrences/2026-12-22");
  });

  it("shows no list when the series has no exceptions", async () => {
    stubCalendarServer({ appointments: [{ ...series, exceptions: [] }] });
    renderWithProviders(<AppointmentSheet opened onClose={() => undefined} appointmentId={1001} />);

    await screen.findByDisplayValue("Kickoff with Nordlys AS");
    expect(screen.queryByRole("button", { name: "Put back" })).not.toBeInTheDocument();
  });
});

describe("AppointmentSheet — an attendee", () => {
  it("shows the appointment read-only and offers the three answers and nothing else", async () => {
    const fetchMock = stubCalendarServer({
      appointments: [
        appointmentFixture({
          owner: KARI,
          myStatus: "needs-action",
          attendees: [{ user: { id: ME, displayName: "Test User" }, status: "needs-action" }],
        }),
      ],
    });
    renderWithProviders(<AppointmentSheet opened onClose={() => undefined} appointmentId={1001} />);

    expect(await screen.findByText("Kickoff with Nordlys AS")).toBeInTheDocument();
    expect(screen.getByText("Organiser: Kari Nordmann")).toBeInTheDocument();
    const buttons = screen.getAllByRole("button").map((button) => button.textContent);
    expect(buttons).toEqual(expect.arrayContaining(["Accept", "Tentative", "Decline"]));
    expect(buttons).not.toContain("Save");
    expect(buttons).not.toContain("Delete");
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Accept" }));
    await waitFor(() => expect(sent(fetchMock, "POST").body).toBeDefined());
    expect(sent(fetchMock, "POST")).toMatchObject({
      url: "/api/v1/calendar/appointments/1001/response",
      body: { status: "accepted" },
    });
  });
});
