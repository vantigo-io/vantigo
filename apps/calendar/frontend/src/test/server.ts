import type { Appointment } from "../api/appointments";
import type { CalendarItem, CalendarItems } from "../api/items";
import type { CalendarMeta } from "../api/meta";
import type { CalendarUser } from "../api/users";
import { jsonResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";

export const ME = "11111111-1111-4111-8111-111111111111";
export const KARI = { id: "22222222-2222-4222-8222-222222222222", displayName: "Kari Nordmann" };
export const OLA = { id: "33333333-3333-4333-8333-333333333333", displayName: "Ola Hansen" };

export const SOURCES = [
  { kind: "customers.follow-up", module: "customers" },
  { kind: "projects.task", module: "projects" },
];

export const defaultMeta = (overrides: Partial<CalendarMeta> = {}): CalendarMeta => ({
  timeZone: "Europe/Oslo",
  today: "2026-10-19",
  canViewColleagues: false,
  invitationsAvailable: true,
  sources: SOURCES,
  ...overrides,
});

export const appointmentFixture = (overrides: Partial<Appointment> = {}): Appointment => ({
  id: 1001,
  title: "Kickoff with Nordlys AS",
  allDay: false,
  startsAt: "2026-10-20T07:00:00Z",
  endsAt: "2026-10-20T08:00:00Z",
  timeZone: "Europe/Oslo",
  visibility: "shared",
  transparency: "busy",
  owner: { id: ME, displayName: "Test User" },
  attendees: [],
  exceptions: [],
  revision: 3,
  createdAt: "2026-10-01T10:00:00Z",
  updatedAt: "2026-10-01T10:00:00Z",
  ...overrides,
});

export interface CalendarServer {
  meta?: CalendarMeta;
  /** What `/items` answers, whatever the window; the window asked for is echoed back. */
  items?: CalendarItem[];
  publicHolidays?: CalendarItems["publicHolidays"];
  colleagueItems?: CalendarItem[];
  users?: CalendarUser[];
  appointments?: Appointment[];
  /** A response to a write, by method, instead of the default success. */
  refuse?: Partial<Record<"POST" | "PUT" | "DELETE", Response>>;
}

/**
 * A fake of the calendar API: reads answered from the fixtures, writes
 * answered with success (or the refusal the test asks for), every request
 * recorded on the returned mock.
 */
export const stubCalendarServer = (server: CalendarServer = {}): StubbedFetch =>
  stubFetch(
    (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      const method = init?.method ?? "GET";
      const path = url.pathname.replace("/api/v1/calendar", "");
      const window = {
        from: url.searchParams.get("from") ?? "",
        to: url.searchParams.get("to") ?? "",
        timeZone: url.searchParams.get("timeZone") ?? "",
      };
      if (method === "GET" && path === "/meta") return Promise.resolve(jsonResponse(200, server.meta ?? defaultMeta()));
      if (method === "GET" && path === "/items")
        return Promise.resolve(
          jsonResponse(200, {
            ...window,
            sources: (server.meta ?? defaultMeta()).sources,
            publicHolidays: server.publicHolidays ?? [],
            items: server.items ?? [],
          } satisfies CalendarItems),
        );
      if (method === "GET" && /^\/colleagues\/[^/]+\/items$/.test(path))
        return Promise.resolve(
          jsonResponse(200, {
            ...window,
            sources: [],
            publicHolidays: server.publicHolidays ?? [],
            items: server.colleagueItems ?? [],
          } satisfies CalendarItems),
        );
      if (method === "GET" && path === "/users") {
        const term = (url.searchParams.get("query") ?? "").toLowerCase();
        return Promise.resolve(
          jsonResponse(
            200,
            (server.users ?? [KARI, OLA]).filter((user) => user.displayName.toLowerCase().includes(term)),
          ),
        );
      }
      const appointmentMatch = /^\/appointments\/(\d+)/.exec(path);
      const appointment = appointmentMatch
        ? (server.appointments ?? []).find((one) => one.id === Number(appointmentMatch[1]))
        : undefined;
      if (method === "GET" && appointmentMatch)
        return Promise.resolve(appointment ? jsonResponse(200, appointment) : new Response(null, { status: 404 }));
      const refusal = server.refuse?.[method as "POST" | "PUT" | "DELETE"];
      if (refusal) return Promise.resolve(refusal);
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      if (method === "POST" && path === "/appointments")
        return Promise.resolve(jsonResponse(201, appointmentFixture({ ...body, id: 2001, attendees: [] })));
      if (method === "POST" && path.endsWith("/response"))
        return Promise.resolve(jsonResponse(200, { ...appointment, myStatus: body.status }));
      if (method === "PUT" && /\/occurrences\//.test(path))
        return Promise.resolve(jsonResponse(200, { occurrenceDate: path.split("/").pop(), cancelled: false, ...body }));
      if (method === "PUT" && appointmentMatch)
        return Promise.resolve(
          jsonResponse(200, { ...appointment, ...body, revision: (appointment?.revision ?? 0) + 1 }),
        );
      if (method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(new Response(null, { status: 404 }));
    },
    { session: { user: { id: ME, displayName: "Test User", email: "test@example.com", roles: ["user"] } } },
  );
