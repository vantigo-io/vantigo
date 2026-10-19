import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { civilDateIn, instantFromWallClock, setLanguagePreference, setTimeZones } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, onTestFinished, vi } from "vitest";
import type { CalendarItem } from "../api/items";
import "../i18n";
import { addDays, daysBetween } from "../lib/dates";
import { HIDDEN_KINDS_STORAGE_KEY } from "../lib/filter";
import { renderRoute } from "../test/route-tree";
import { defaultMeta, KARI, stubCalendarServer } from "../test/server";

const ZONE = "Europe/Oslo";
const today = () => civilDateIn(ZONE);
const at = (offset: number, time: string) => instantFromWallClock(addDays(today(), offset), time, ZONE).toISOString();

/** What `/items` answers, around today in the session's zone. */
const items = (): CalendarItem[] => [
  {
    kind: "calendar.appointment",
    id: "1001",
    title: "Kickoff with Nordlys AS",
    allDay: false,
    startsAt: at(0, "09:00"),
    endsAt: at(0, "10:30"),
    done: false,
    appointment: { id: 1001, isOwner: true, recurring: false, transparency: "busy", visibility: "shared" },
  },
  {
    kind: "customers.follow-up",
    id: "55",
    title: "Call back Nordlys",
    subtitle: "Nordlys AS",
    allDay: true,
    startDate: today(),
    endDate: today(),
    done: false,
    path: "/customers/42",
  },
  {
    kind: "projects.task",
    id: "8",
    title: "Trade fair stand",
    allDay: true,
    startDate: addDays(today(), 1),
    endDate: addDays(today(), 3),
    done: true,
    path: "/projects/3/tasks?task=8",
  },
];

/** The windows `/items` was asked for. */
const windowsAsked = (fetchMock: ReturnType<typeof stubCalendarServer>) =>
  fetchMock.actualCalls
    .map(([url]) => new URL(String(url), "http://localhost"))
    .filter((url) => url.pathname === "/api/v1/calendar/items")
    .map((url) => Object.fromEntries(url.searchParams));

beforeEach(() => {
  window.localStorage.removeItem(HIDDEN_KINDS_STORAGE_KEY);
  // The session's zone is Oslo, the machine's New York (vite.config.ts).
  setTimeZones({ timeZone: ZONE, timeZoneSource: "personal", businessTimeZone: ZONE });
});

afterEach(() => {
  setTimeZones(null);
  setLanguagePreference("auto");
});

describe("CalendarPage", () => {
  it("draws the month from /items, asked for the grid's window in the session's zone", async () => {
    const fetchMock = stubCalendarServer({ items: items() });
    renderRoute("/calendar");

    expect(await screen.findByText("Kickoff with Nordlys AS")).toBeInTheDocument();
    expect(screen.getByText("Call back Nordlys")).toBeInTheDocument();
    expect(screen.getAllByText("Trade fair stand").length).toBeGreaterThan(0);
    expect(screen.getByRole("radio", { name: "Month" })).toBeChecked();
    const [window] = windowsAsked(fetchMock);
    expect(window.timeZone).toBe(ZONE);
    // The month grid's six weeks, today among them.
    expect(daysBetween(window.from, window.to)).toBe(41);
    expect(window.from <= today() && today() <= window.to).toBe(true);
  });

  it("switches to the week's time grid, times as 09:00 in the session's zone", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer({ items: items() });
    const { router } = renderRoute("/calendar");
    await screen.findByText("Kickoff with Nordlys AS");

    await user.click(screen.getByText("Week"));

    expect(await screen.findByText(/09:00\s*–\s*10:30/)).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Week" })).toBeChecked();
    expect(router.state.location.search).toMatchObject({ view: "week" });
    await waitFor(() =>
      expect(daysBetween(windowsAsked(fetchMock).at(-1)?.from ?? "", windowsAsked(fetchMock).at(-1)?.to ?? "")).toBe(6),
    );
  });

  it("lists the items in the list view", async () => {
    stubCalendarServer({ items: items() });
    renderRoute("/calendar?view=list");

    expect(await screen.findByText("Kickoff with Nordlys AS")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "List" })).toBeChecked();
    expect(screen.getByText("Call back Nordlys")).toBeInTheDocument();
  });

  it("opens on the list under phone width", async () => {
    // The setup's matchMedia is a plain mock, which restoreAllMocks leaves as it is: put it back.
    const desktop = vi.mocked(window.matchMedia).getMockImplementation();
    onTestFinished(() => {
      if (desktop) vi.mocked(window.matchMedia).mockImplementation(desktop);
    });
    vi.mocked(window.matchMedia).mockImplementation(
      (query: string) =>
        ({
          matches: query.includes("max-width"),
          media: query,
          onchange: null,
          addListener: vi.fn(),
          removeListener: vi.fn(),
          addEventListener: vi.fn(),
          removeEventListener: vi.fn(),
          dispatchEvent: vi.fn(),
        }) as unknown as MediaQueryList,
    );
    stubCalendarServer({ items: items() });
    renderRoute("/calendar");

    expect(await screen.findByText("Kickoff with Nordlys AS")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "List" })).toBeChecked();
  });

  it("hides a kind from the legend and remembers it", async () => {
    const user = userEvent.setup();
    stubCalendarServer({ items: items() });
    const first = renderRoute("/calendar");
    await screen.findByText("Call back Nordlys");
    const legend = screen.getByRole("group", { name: "Shown on the calendar" });
    expect(within(legend).getByText("Appointments")).toBeInTheDocument();
    expect(within(legend).getByText("Tasks")).toBeInTheDocument();
    expect(within(legend).getByText("Public holidays")).toBeInTheDocument();

    await user.click(within(legend).getByText("Follow-ups"));

    await waitFor(() => expect(screen.queryByText("Call back Nordlys")).not.toBeInTheDocument());
    expect(screen.getByText("Kickoff with Nordlys AS")).toBeInTheDocument();
    expect(JSON.parse(window.localStorage.getItem(HIDDEN_KINDS_STORAGE_KEY) ?? "[]")).toEqual(["customers.follow-up"]);

    first.unmount();
    renderRoute("/calendar");
    await screen.findByText("Kickoff with Nordlys AS");
    expect(screen.queryByText("Call back Nordlys")).not.toBeInTheDocument();
  });

  it("follows a projected item to its record", async () => {
    const user = userEvent.setup();
    stubCalendarServer({ items: items() });
    const { router } = renderRoute("/calendar");

    await user.click(await screen.findByText("Call back Nordlys"));

    await waitFor(() => expect(router.state.location.pathname).toBe("/customers/42"));
  });

  it("draws a subscription's items in their colours, opens nothing on a click, and hides them with one chip", async () => {
    const user = userEvent.setup();
    const subscriptionItems: CalendarItem[] = [
      {
        kind: "calendar.subscription",
        id: "1001:training",
        title: "Training",
        subtitle: "Football club",
        allDay: false,
        startsAt: at(0, "18:00"),
        endsAt: at(0, "19:30"),
        done: false,
        subscription: { id: 1001, name: "Football club", colour: "lime", busyOnly: false },
      },
      {
        kind: "calendar.subscription",
        id: "1002:x",
        title: "",
        subtitle: "Partner",
        allDay: true,
        startDate: today(),
        endDate: today(),
        done: false,
        subscription: { id: 1002, name: "Partner", colour: "indigo", busyOnly: true },
      },
    ];
    stubCalendarServer({
      meta: defaultMeta({
        sources: [{ kind: "calendar.subscription", module: "calendar" }, ...defaultMeta().sources],
      }),
      items: [...items(), ...subscriptionItems],
    });
    const { router } = renderRoute("/calendar");

    const training = (await screen.findByText("Training")).closest(".vantigo-calendar-item") as HTMLElement;
    expect(training.getAttribute("style")).toContain("--vantigo-calendar-lime");
    expect(training).toHaveAttribute("title", "Training · Football club");
    const busy = screen.getByText("Busy").closest(".vantigo-calendar-item") as HTMLElement;
    expect(busy.getAttribute("style")).toContain("--vantigo-calendar-indigo");

    await user.click(screen.getByText("Training"));
    expect(router.state.location.pathname).toBe("/calendar");
    expect(router.state.location.search).not.toHaveProperty("appointment");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    const legend = screen.getByRole("group", { name: "Shown on the calendar" });
    await user.click(within(legend).getByText("Subscriptions"));
    await waitFor(() => expect(screen.queryByText("Training")).not.toBeInTheDocument());
    expect(screen.queryByText("Busy")).not.toBeInTheDocument();
    expect(screen.getByText("Kickoff with Nordlys AS")).toBeInTheDocument();
  });

  it("opens the sheet of an appointment clicked on the grid", async () => {
    const user = userEvent.setup();
    stubCalendarServer({ items: items() });
    const { router } = renderRoute("/calendar");

    await user.click(await screen.findByText("Kickoff with Nordlys AS"));

    await waitFor(() => expect(router.state.location.search).toMatchObject({ appointment: 1001 }));
    expect(await screen.findByRole("dialog", { name: "Appointment" })).toBeInTheDocument();
  });

  it("opens a new appointment's sheet with the customer it is made from", async () => {
    stubCalendarServer({ items: [] });
    renderRoute("/calendar?new=1&customerId=42");

    const sheet = await screen.findByRole("dialog", { name: "New appointment" });
    expect(within(sheet).getByText("Customer: no. 42")).toBeInTheDocument();
  });

  it("swaps to a colleague's calendar, read-only, a private appointment named Busy", async () => {
    const user = userEvent.setup();
    const fetchMock = stubCalendarServer({
      meta: defaultMeta({ canViewColleagues: true }),
      items: items(),
      colleagueItems: [
        {
          kind: "calendar.appointment",
          id: "3001",
          title: "",
          allDay: false,
          startsAt: at(0, "13:00"),
          endsAt: at(0, "14:00"),
          done: false,
          appointment: { id: 3001, isOwner: false, recurring: false, transparency: "busy", visibility: "private" },
        },
        {
          kind: "calendar.appointment",
          id: "3002",
          title: "Budget review",
          allDay: false,
          startsAt: at(0, "15:00"),
          endsAt: at(0, "16:00"),
          done: false,
          appointment: { id: 3002, isOwner: false, recurring: false, transparency: "busy", visibility: "shared" },
        },
      ],
    });
    const { router } = renderRoute("/calendar");
    await screen.findByText("Kickoff with Nordlys AS");

    await user.click(screen.getByRole("combobox", { name: "Whose calendar" }));
    await user.click(await screen.findByRole("option", { name: KARI.displayName }));

    expect(await screen.findByText("Busy")).toBeInTheDocument();
    expect(screen.getByText("Budget review")).toBeInTheDocument();
    expect(screen.queryByText("Kickoff with Nordlys AS")).not.toBeInTheDocument();
    expect(router.state.location.search).toMatchObject({ user: KARI.id });
    expect(
      fetchMock.actualCalls.some(([url]) => String(url).startsWith(`/api/v1/calendar/colleagues/${KARI.id}/items?`)),
    ).toBe(true);
    // The legend is reduced to the appointments and the holidays.
    const legend = screen.getByRole("group", { name: "Shown on the calendar" });
    expect(within(legend).queryByText("Tasks")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New appointment" })).not.toBeInTheDocument();
  });

  it("speaks Norwegian, controls and grid alike", async () => {
    setLanguagePreference("nb");
    const user = userEvent.setup();
    stubCalendarServer({ items: items() });
    renderRoute("/calendar");
    await screen.findByText("Kickoff with Nordlys AS");

    expect(screen.getByRole("heading", { name: "Kalender" })).toBeInTheDocument();
    expect((await screen.findAllByText("man.")).length).toBeGreaterThan(0);
    await user.click(screen.getByText("Uke"));

    expect(await screen.findByText(/Hele\s+dagen/)).toBeInTheDocument();
    expect(screen.getByText(/09:00\s*–\s*10:30/)).toBeInTheDocument();
  });
});
