import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RsvpPageView } from "../api/rsvp";
import { calendarCatalog } from "../i18n";
import { jsonResponse, problemResponse } from "../test/api";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { RSVP_STORAGE_KEY, RsvpPage } from "./rsvp";

// A token with characters a URL encodes, so the encoded check below means something.
const TOKEN = "tok-a+b/c=_Z";
const LINK = `/rsvp?token=${encodeURIComponent(TOKEN)}`;
const READ = "/api/v1/calendar/rsvp/read";
const ANSWER = "/api/v1/calendar/rsvp";

const en = calendarCatalog.en.rsvp;
const nb = calendarCatalog.nb.rsvp;

const rsvpPage = (overrides: Partial<RsvpPageView> = {}): RsvpPageView => ({
  title: "Kickoff with Nordlys AS",
  allDay: false,
  // 09:00–10:00 in Oslo on 3 November (winter time, UTC+1); 03:00–04:00 in New York.
  startsAt: "2026-11-03T08:00:00Z",
  endsAt: "2026-11-03T09:00:00Z",
  timeZone: "Europe/Oslo",
  location: "Bergen",
  organiser: { name: "Kari Nordmann" },
  company: "Vantigo Demo AS",
  attendee: { name: "Per Utenfor" },
  status: "needs-action",
  cancelled: false,
  language: "en",
  ...overrides,
});

interface RsvpServer {
  read?: () => Response;
  answer?: (status: string) => Response;
}

/** The two anonymous operations' fake; anything else is a 404 the page must never ask for. */
const server = ({ read, answer }: RsvpServer = {}) =>
  stubFetch(
    (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === READ && method === "POST") return read ? read() : jsonResponse(200, rsvpPage());
      if (url === ANSWER && method === "POST") {
        const { status } = JSON.parse(String(init?.body)) as { status: string };
        return answer ? answer(status) : jsonResponse(200, rsvpPage({ status: status as RsvpPageView["status"] }));
      }
      return new Response(null, { status: 404 });
    },
    { session: "delegate" },
  );

const at = (address: string) => window.history.replaceState(null, "", address);

const bodiesTo = (fetchMock: ReturnType<typeof server>, url: string) =>
  fetchMock.actualCalls.filter(([u]) => String(u) === url).map(([, init]) => JSON.parse(String(init?.body)));

beforeEach(() => window.sessionStorage.clear());

afterEach(() => {
  at("/");
  window.sessionStorage.clear();
  setLanguagePreference("auto");
});

describe("RsvpPage", () => {
  it("reads the token from the address once, keeps it for the tab, clears the bar and posts it", async () => {
    at(LINK);
    const fetchMock = server();
    renderWithProviders(<RsvpPage />);

    expect(await screen.findByRole("heading", { name: "Kickoff with Nordlys AS" })).toBeInTheDocument();
    expect(bodiesTo(fetchMock, READ)).toEqual([{ token: TOKEN }]);
    expect(window.location.search).toBe("");
    expect(window.location.pathname).toBe("/rsvp");
    expect(window.sessionStorage.getItem(RSVP_STORAGE_KEY)).toBe(TOKEN);
  });

  it("shows when in the appointment's zone and the browser's, where, who organises and for whom", async () => {
    at(LINK);
    server();
    renderWithProviders(<RsvpPage />);

    expect(await screen.findByText("Tuesday, November 3, 2026, 09:00 – 10:00 (Europe/Oslo)")).toBeInTheDocument();
    // The suite's browser is in New York (vite.config.ts): six hours behind Oslo.
    expect(screen.getByText("(03:00 – 04:00 in America/New_York)")).toBeInTheDocument();
    expect(screen.getByText("Where: Bergen")).toBeInTheDocument();
    expect(screen.getByText("Organised by Kari Nordmann, Vantigo Demo AS")).toBeInTheDocument();
    expect(screen.getByText("Invitation for Per Utenfor")).toBeInTheDocument();
    for (const name of [en.accept, en.tentative, en.decline])
      expect(screen.getByRole("button", { name })).toHaveAttribute("aria-pressed", "false");
  });

  it("shows an all-day appointment as its days, with no second zone", async () => {
    at(LINK);
    server({
      read: () =>
        jsonResponse(
          200,
          rsvpPage({
            allDay: true,
            startsAt: undefined,
            endsAt: undefined,
            startDate: "2026-11-03",
            endDate: "2026-11-04",
            company: undefined,
          }),
        ),
    });
    renderWithProviders(<RsvpPage />);

    expect(await screen.findByText("Nov 3, 2026 – Nov 4, 2026")).toBeInTheDocument();
    expect(screen.queryByText(/America\/New_York/)).not.toBeInTheDocument();
    expect(screen.getByText("Organised by Kari Nordmann")).toBeInTheDocument();
  });

  it("speaks the appointment's language, not the browser's", async () => {
    at(LINK);
    setLanguagePreference("en");
    server({ read: () => jsonResponse(200, rsvpPage({ language: "nb", status: "accepted" })) });
    renderWithProviders(<RsvpPage />);

    expect(await screen.findByText("Arrangert av Kari Nordmann, Vantigo Demo AS")).toBeInTheDocument();
    expect(screen.getByText("Invitasjon til Per Utenfor")).toBeInTheDocument();
    expect(screen.getByText("tirsdag 3. november 2026, 09:00 – 10:00 (Europe/Oslo)")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: nb.accept })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: nb.decline })).toHaveAttribute("aria-pressed", "false");
    expect(screen.queryByRole("button", { name: en.accept })).not.toBeInTheDocument();
  });

  it("answers with the token and the answer in the body, and thanks the invitee", async () => {
    at(LINK);
    const fetchMock = server();
    renderWithProviders(<RsvpPage />);

    await userEvent.click(await screen.findByRole("button", { name: en.decline }));

    expect(await screen.findByText(en.thanks)).toBeInTheDocument();
    expect(screen.getByText("Your answer: Declined")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: en.decline })).toHaveAttribute("aria-pressed", "true");
    expect(bodiesTo(fetchMock, ANSWER)).toEqual([{ token: TOKEN, status: "declined" }]);
  });

  it("reads the token the tab kept when the address no longer has it", async () => {
    window.sessionStorage.setItem(RSVP_STORAGE_KEY, TOKEN);
    at("/rsvp");
    const fetchMock = server();
    renderWithProviders(<RsvpPage />);

    expect(await screen.findByRole("heading", { name: "Kickoff with Nordlys AS" })).toBeInTheDocument();
    expect(bodiesTo(fetchMock, READ)).toEqual([{ token: TOKEN }]);
  });

  it("says a cancelled appointment is cancelled, and offers no answer", async () => {
    at(LINK);
    server({ read: () => jsonResponse(200, rsvpPage({ cancelled: true })) });
    renderWithProviders(<RsvpPage />);

    expect(await screen.findByText(en.cancelled)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Kickoff with Nordlys AS" })).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("reads the page again as cancelled when the answer is refused for it", async () => {
    at(LINK);
    let cancelled = false;
    const fetchMock = server({
      read: () => jsonResponse(200, rsvpPage({ cancelled })),
      answer: () => {
        cancelled = true;
        return problemResponse(409, "Cancelled", { code: "appointment_cancelled" });
      },
    });
    renderWithProviders(<RsvpPage />);

    await userEvent.click(await screen.findByRole("button", { name: en.accept }));

    expect(await screen.findByText(en.cancelled)).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(bodiesTo(fetchMock, READ)).toHaveLength(2);
  });

  it("says an address with no token is not a valid link, and asks for nothing", async () => {
    at("/rsvp");
    const fetchMock = server();
    renderWithProviders(<RsvpPage />);

    expect(await screen.findByText(en.invalid)).toBeInTheDocument();
    expect(fetchMock.actualCalls).toHaveLength(0);
  });

  it("says a token nobody holds is not a valid link", async () => {
    at(LINK);
    server({ read: () => new Response(null, { status: 404 }) });
    renderWithProviders(<RsvpPage />);

    expect(await screen.findByText(en.invalid)).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("hands the clearing of the address to the host when it asks to, once", async () => {
    at(LINK);
    server();
    const clearAddress = vi.fn();
    renderWithProviders(<RsvpPage clearAddress={clearAddress} />);

    await screen.findByRole("heading", { name: "Kickoff with Nordlys AS" });
    expect(clearAddress).toHaveBeenCalledTimes(1);
  });

  it("never carries the token in a URL, from the read through the answer", async () => {
    at(LINK);
    const fetchMock = server();
    renderWithProviders(<RsvpPage />);

    await userEvent.click(await screen.findByRole("button", { name: en.accept }));
    await screen.findByText(en.thanks);

    await waitFor(() => expect(fetchMock.calls.length).toBeGreaterThanOrEqual(2));
    const urls = fetchMock.calls.map(([url]) => String(url));
    expect(urls).toEqual([READ, ANSWER]);
    expect(urls.some((url) => url.includes(TOKEN) || url.includes(encodeURIComponent(TOKEN)))).toBe(false);
    expect(window.location.href).not.toContain(encodeURIComponent(TOKEN));
  });
});
