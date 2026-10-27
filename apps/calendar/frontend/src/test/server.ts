import type { Appointment } from "../api/appointments";
import type { CalendarContact } from "../api/contacts";
import type { FeedAddress, FeedAddresses, FeedVariant } from "../api/feed";
import type { CalendarItem, CalendarItems } from "../api/items";
import type { MailboxSync, MailboxSyncWrite } from "../api/mailbox-sync";
import type { CalendarMeta } from "../api/meta";
import type { CalendarSubscription } from "../api/subscriptions";
import type { CalendarUser } from "../api/users";
import { jsonResponse, problemResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";

export const ME = "11111111-1111-4111-8111-111111111111";
export const KARI = { id: "22222222-2222-4222-8222-222222222222", displayName: "Kari Nordmann" };
export const OLA = { id: "33333333-3333-4333-8333-333333333333", displayName: "Ola Hansen" };

export const SIRI: CalendarContact = { id: 7, name: "Siri Kunde", email: "siri@nordlys.example" };
export const TORE: CalendarContact = { id: 8, name: "Tore Innkjøp", email: "tore@nordlys.example" };

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

export const feedAddressFixture = (variant: FeedVariant, overrides: Partial<FeedAddress> = {}): FeedAddress => ({
  variant,
  address: `https://vantigo.example/api/v1/calendar/feed/${variant}-token.ics`,
  webcalAddress: `webcal://vantigo.example/api/v1/calendar/feed/${variant}-token.ics`,
  createdAt: "2026-11-03T09:00:00Z",
  ...overrides,
});

export const subscriptionFixture = (overrides: Partial<CalendarSubscription> = {}): CalendarSubscription => ({
  id: 1001,
  name: "Football club",
  url: "https://club.example/calendar.ics",
  colour: "teal",
  busyOnly: false,
  fetchedAt: "2026-11-03T09:00:00Z",
  attemptedAt: "2026-11-03T09:00:00Z",
  createdAt: "2026-11-01T09:00:00Z",
  updatedAt: "2026-11-01T09:00:00Z",
  ...overrides,
});

/** The Outlook sync as a caller who never turned it on is answered it: the defaults at revision 0. */
export const mailboxSyncFixture = (overrides: Partial<MailboxSync> = {}): MailboxSync => ({
  availability: "available",
  availableKinds: [
    { key: "calendar.appointment", module: "calendar" },
    { key: "staff.absence", module: "staff" },
    { key: "projects.task", module: "projects" },
  ],
  enabled: false,
  target: "default",
  detail: "full",
  kinds: ["calendar.appointment", "staff.absence"],
  state: "off",
  revision: 0,
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
  /** What `/contacts` answers for any customer: the contacts, or a refusal. */
  contacts?: CalendarContact[] | (() => Response);
  /** What `/feed` answers at first; a POST puts a new address in, a DELETE takes it out. */
  feed?: FeedAddresses;
  /** What `/subscriptions` answers at first; the writes change it as the server would. */
  subscriptions?: CalendarSubscription[];
  /** What the inline fetch of a POST leaves the new row as, beside what was written. */
  created?: Partial<CalendarSubscription>;
  /** What a refresh leaves the row as; by default fetched now, without an error. */
  refreshed?: Partial<CalendarSubscription>;
  /** A response to a write, by method, instead of the default success. */
  refuse?: Partial<Record<"POST" | "PUT" | "DELETE", Response>>;
  /** What `/mailbox-sync` answers at first; a PUT replaces it under If-Match, as the server would. */
  mailboxSync?: MailboxSync;
  /** A response to `PUT /mailbox-sync` instead of the fake's own. */
  mailboxSyncRefusal?: Response;
}

/**
 * A fake of the calendar API: reads answered from the fixtures, writes
 * answered with success (or the refusal the test asks for), every request
 * recorded on the returned mock.
 */
export const stubCalendarServer = (server: CalendarServer = {}): StubbedFetch => {
  let feed: FeedAddresses = server.feed ?? { public: true, addresses: [] };
  let subscriptions = [...(server.subscriptions ?? [])];
  let resets = 0;
  let mailboxSync = server.mailboxSync ?? mailboxSyncFixture();
  return stubFetch(
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
      if (method === "GET" && path === "/feed") return Promise.resolve(jsonResponse(200, feed));
      if (method === "GET" && path === "/subscriptions") return Promise.resolve(jsonResponse(200, subscriptions));
      if (method === "GET" && path === "/contacts")
        return Promise.resolve(
          typeof server.contacts === "function"
            ? server.contacts()
            : jsonResponse(200, server.contacts ?? [SIRI, TORE]),
        );
      const appointmentMatch = /^\/appointments\/(\d+)/.exec(path);
      const appointment = appointmentMatch
        ? (server.appointments ?? []).find((one) => one.id === Number(appointmentMatch[1]))
        : undefined;
      if (method === "GET" && appointmentMatch)
        return Promise.resolve(appointment ? jsonResponse(200, appointment) : new Response(null, { status: 404 }));
      if (path === "/mailbox-sync" && method === "GET") return Promise.resolve(jsonResponse(200, mailboxSync));
      if (path === "/mailbox-sync" && method === "PUT") {
        if (server.mailboxSyncRefusal) return Promise.resolve(server.mailboxSyncRefusal);
        const ifMatch = Number(new Headers(init?.headers).get("If-Match")?.replaceAll('"', ""));
        if (ifMatch !== mailboxSync.revision)
          return Promise.resolve(problemResponse(409, "Conflict", { code: "revision_mismatch" }));
        const write = JSON.parse(String(init?.body)) as MailboxSyncWrite;
        if (write.enabled && mailboxSync.availability !== "available")
          return Promise.resolve(
            problemResponse(409, "Conflict", { code: "mailbox_sync_unavailable", detail: mailboxSync.availability }),
          );
        const state = write.enabled ? (mailboxSync.enabled ? mailboxSync.state : "pending") : "stopping";
        mailboxSync = { ...mailboxSync, ...write, state, revision: mailboxSync.revision + 1 };
        return Promise.resolve(jsonResponse(200, mailboxSync));
      }
      if (path === "/mailbox-sync/run" && method === "POST") {
        if (!mailboxSync.enabled || mailboxSync.availability !== "available")
          return Promise.resolve(
            problemResponse(409, "Conflict", { code: "mailbox_sync_unavailable", detail: mailboxSync.availability }),
          );
        return Promise.resolve(jsonResponse(200, mailboxSync));
      }
      const refusal = server.refuse?.[method as "POST" | "PUT" | "DELETE"];
      if (refusal) return Promise.resolve(refusal);
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      const feedMatch = /^\/feed\/(full|busy)$/.exec(path);
      if (method === "POST" && feedMatch) {
        resets += 1;
        const variant = feedMatch[1] as FeedVariant;
        const address = feedAddressFixture(variant, {
          address: `https://vantigo.example/api/v1/calendar/feed/${variant}-token-${resets}.ics`,
          webcalAddress: `webcal://vantigo.example/api/v1/calendar/feed/${variant}-token-${resets}.ics`,
        });
        feed = { ...feed, addresses: [...feed.addresses.filter((one) => one.variant !== variant), address] };
        return Promise.resolve(jsonResponse(200, address));
      }
      if (method === "DELETE" && feedMatch) {
        feed = { ...feed, addresses: feed.addresses.filter((one) => one.variant !== feedMatch[1]) };
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      const subscriptionMatch = /^\/subscriptions\/(\d+)(\/refresh)?$/.exec(path);
      const subscription = subscriptionMatch
        ? subscriptions.find((one) => one.id === Number(subscriptionMatch[1]))
        : undefined;
      if (subscriptionMatch && !subscription) return Promise.resolve(new Response(null, { status: 404 }));
      const replaceSubscription = (row: CalendarSubscription) => {
        subscriptions = subscriptions.map((one) => (one.id === row.id ? row : one));
        return Promise.resolve(jsonResponse(200, row));
      };
      if (method === "POST" && path === "/subscriptions") {
        const row = subscriptionFixture({ ...body, id: 2001 + subscriptions.length, ...server.created });
        subscriptions = [...subscriptions, row];
        return Promise.resolve(jsonResponse(201, row));
      }
      if (method === "POST" && subscriptionMatch?.[2] && subscription) {
        // A fetch that worked clears the last one's error.
        return replaceSubscription({
          ...subscription,
          error: undefined,
          fetchedAt: new Date().toISOString(),
          ...server.refreshed,
        });
      }
      if (method === "PUT" && subscriptionMatch && subscription)
        return replaceSubscription({ ...subscription, ...body });
      if (method === "DELETE" && subscriptionMatch) {
        subscriptions = subscriptions.filter((one) => one.id !== subscription?.id);
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (method === "POST" && path === "/appointments")
        return Promise.resolve(jsonResponse(201, appointmentFixture({ ...body, id: 2001, attendees: [] })));
      const resend = /\/attendees\/(\d+)\/resend$/.exec(path);
      if (method === "POST" && resend && appointment)
        return Promise.resolve(
          jsonResponse(200, {
            ...appointment,
            attendees: appointment.attendees.map((attendee) =>
              attendee.id === Number(resend[1])
                ? { ...attendee, invitation: { sentAt: "2026-11-04T09:00:00Z", sequence: 0 } }
                : attendee,
            ),
          }),
        );
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
};
