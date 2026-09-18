import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { TimelineEntry } from "../api/projects";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { ProjectTimeline } from "./-project-timeline";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** The generated payload type is an empty record, so entries are built loosely and cast once. */
const entry = (overrides: Partial<Omit<TimelineEntry, "payload">> & { payload?: Record<string, unknown> }) =>
  ({
    id: 1,
    eventType: "project-created",
    actorDisplay: "Ada Lovelace",
    actorUserId: "11111111-1111-1111-1111-111111111111",
    occurredAt: "2026-01-02T09:30:00Z",
    payload: {},
    ...overrides,
  }) as TimelineEntry;

const stubTimeline = (entries: TimelineEntry[], status = 200, totalPages = 1) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname !== "/api/v1/projects/7/timeline") return Promise.resolve(new Response(null, { status: 404 }));
    if (status !== 200) return Promise.resolve(jsonResponse(status, { title: "Timeline is unavailable" }));
    return Promise.resolve(
      jsonResponse(200, {
        data: entries,
        pagination: {
          page: Number(url.searchParams.get("page") ?? 1),
          pageSize: 20,
          totalCount: entries.length,
          totalPages,
          hasNextPage: totalPages > 1,
          hasPreviousPage: false,
        },
      }),
    );
  });

describe("ProjectTimeline", () => {
  it("reads a code change as the codes it went from and to", async () => {
    stubTimeline([entry({ eventType: "code-changed", payload: { old: "KVEWEB", new: "KVEWEBS" } })]);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("Code changed from KVEWEB to KVEWEBS.")).toBeInTheDocument();
    expect(screen.getByText(/Ada Lovelace/)).toBeInTheDocument();
  });

  it("names the statuses and roles of an event in the reader's own words", async () => {
    stubTimeline([
      entry({ id: 2, eventType: "status-changed", payload: { old: "planned", new: "on-hold" } }),
      entry({
        id: 3,
        eventType: "role-added",
        payload: { userId: "2", displayName: "Alan Turing", role: "manager" },
      }),
    ]);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("Status changed from Planned to On hold.")).toBeInTheDocument();
    expect(screen.getByText("Alan Turing was added as Manager.")).toBeInTheDocument();
  });

  it("falls back to the raw event type it does not know", async () => {
    stubTimeline([entry({ id: 4, eventType: "something-new", payload: {} })]);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("something-new")).toBeInTheDocument();
  });

  it("says that nothing has happened yet", async () => {
    stubTimeline([]);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("Nothing has happened on this project yet.")).toBeInTheDocument();
  });

  it("reports a timeline that cannot be read", async () => {
    stubTimeline([], 500);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("Could not load the timeline")).toBeInTheDocument();
  });

  it("pages a timeline longer than one page", async () => {
    stubTimeline([entry({ eventType: "line-deactivated", payload: { code: "PM" } })], 200, 3);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("Billing line PM was deactivated.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "2" })).toBeInTheDocument();
  });
});
