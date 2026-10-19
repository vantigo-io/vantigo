import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { civilDateIn, instantFromWallClock, setTimeZones } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { CalendarItem } from "../api/items";
import "../i18n";
import { addDays } from "../lib/dates";
import { renderRoute } from "../test/route-tree";
import { stubCalendarServer } from "../test/server";
import { AgendaWidget } from "./agenda-widget";

const ZONE = "Europe/Oslo";
const today = () => civilDateIn(ZONE);
const at = (offset: number, time: string) => instantFromWallClock(addDays(today(), offset), time, ZONE).toISOString();

const timed = (id: string, title: string, offset: number, start: string, end: string): CalendarItem => ({
  kind: "calendar.appointment",
  id,
  title,
  allDay: false,
  startsAt: at(offset, start),
  endsAt: at(offset, end),
  done: false,
  appointment: { id: Number(id), isOwner: true, recurring: false, transparency: "busy", visibility: "shared" },
});

const week = (): CalendarItem[] => [
  // Out of order on purpose: the widget sorts all-day first, then by start.
  timed("1002", "Lunch with Kari", 0, "12:00", "13:00"),
  timed("1001", "Standup", 0, "09:00", "09:15"),
  {
    kind: "customers.follow-up",
    id: "55",
    title: "Call back Nordlys",
    allDay: true,
    startDate: today(),
    endDate: today(),
    done: false,
    path: "/customers/42",
  },
  timed("1003", "Site visit", 1, "08:00", "11:00"),
  timed("1004", "Review", 1, "14:00", "15:00"),
  timed("1005", "Planning", 3, "10:00", "11:00"),
];

beforeEach(() => setTimeZones({ timeZone: ZONE, timeZoneSource: "personal", businessTimeZone: ZONE }));
afterEach(() => setTimeZones(null));

describe("AgendaWidget", () => {
  it("lists today's items in order with their times, and the next six days collapsed", async () => {
    const fetchMock = stubCalendarServer({ items: week() });
    renderRoute("/", <AgendaWidget />);

    expect(await screen.findByText("Today and this week")).toBeInTheDocument();
    const todaySection = await screen.findByRole("region", { name: "Today" });
    await within(todaySection).findByText("Standup");
    const titles = within(todaySection)
      .getAllByRole("link")
      .map((link) => link.textContent);
    expect(titles).toEqual(["Call back Nordlys", "Standup", "Lunch with Kari"]);
    expect(within(todaySection).getByText("09:00–09:15")).toBeInTheDocument();
    expect(within(todaySection).getByText("All day")).toBeInTheDocument();

    const days = screen.getAllByTestId("agenda-day");
    expect(days).toHaveLength(6);
    expect(days[0]).toHaveTextContent(/· 2 items$/);
    expect(days[1]).toHaveTextContent(/· 0 items$/);
    expect(days[2]).toHaveTextContent(/· 1 item$/);
    expect(screen.queryByText("Site visit")).not.toBeInTheDocument();

    await userEvent.click(days[0]);
    expect(await screen.findByText("Site visit")).toBeInTheDocument();
    expect(screen.getByText("Review")).toBeInTheDocument();

    const [url] = fetchMock.actualCalls.map(([call]) => String(call)).filter((call) => call.includes("/items?"));
    const params = new URL(url, "http://localhost").searchParams;
    expect(Object.fromEntries(params)).toEqual({ from: today(), to: addDays(today(), 6), timeZone: ZONE });
  });

  it("links an appointment to the calendar with its sheet open, and a record to its page", async () => {
    stubCalendarServer({ items: week() });
    const { router } = renderRoute("/", <AgendaWidget />);

    const standup = await screen.findByRole("link", { name: "Standup" });
    expect(standup).toHaveAttribute("href", `/calendar?date=${today()}&appointment=1001`);
    await userEvent.click(screen.getByRole("link", { name: "Call back Nordlys" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/customers/42"));
  });

  it("lists a subscription's item without a link, a busy-only one as Busy", async () => {
    stubCalendarServer({
      items: [
        {
          kind: "calendar.subscription",
          id: "1001:a",
          title: "Training",
          subtitle: "Football club",
          allDay: false,
          startsAt: at(0, "18:00"),
          endsAt: at(0, "19:30"),
          done: false,
          subscription: { id: 1001, name: "Football club", colour: "teal", busyOnly: false },
        },
        {
          kind: "calendar.subscription",
          id: "1002:b",
          title: "",
          subtitle: "Partner",
          allDay: false,
          startsAt: at(0, "20:00"),
          endsAt: at(0, "21:00"),
          done: false,
          subscription: { id: 1002, name: "Partner", colour: "grape", busyOnly: true },
        },
      ],
    });
    renderRoute("/", <AgendaWidget />);

    const todaySection = await screen.findByRole("region", { name: "Today" });
    expect(await within(todaySection).findByText("Training")).toBeInTheDocument();
    expect(within(todaySection).getByText("Football club")).toBeInTheDocument();
    expect(within(todaySection).getByText("Busy")).toBeInTheDocument();
    expect(within(todaySection).queryAllByRole("link")).toHaveLength(0);
  });

  it("says when the week is empty, and offers a new appointment", async () => {
    stubCalendarServer({ items: [] });
    const { router } = renderRoute("/", <AgendaWidget />);

    expect(await screen.findByText("Nothing on your calendar this week.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "New appointment" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/calendar"));
    expect(router.state.location.search).toMatchObject({ new: 1 });
  });
});
