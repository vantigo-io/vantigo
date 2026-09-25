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
  it("names a work type and the fields a change of it moved, never its percentages", async () => {
    stubTimeline([
      // Literally what the server writes: an added type names all three fields.
      entry({
        id: 21,
        eventType: "work-type-added",
        payload: {
          workTypeId: 11,
          name: "Overtid 50 %",
          fields: ["name", "billMultiplierPercent", "costMultiplierPercent"],
        },
      }),
      entry({
        id: 22,
        eventType: "work-type-changed",
        payload: { workTypeId: 11, name: "Overtid 50 %", fields: ["costMultiplierPercent", "active"] },
      }),
    ]);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("Work type Overtid 50 % was added.")).toBeInTheDocument();
    expect(screen.getByText("Work type Overtid 50 % changed: Cost multiplier, Active.")).toBeInTheDocument();
  });

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

  it("names the fields an update moved, on the project and on a line", async () => {
    stubTimeline([
      entry({ id: 5, eventType: "details-changed", payload: { fields: ["description", "startDate"] } }),
      entry({ id: 6, eventType: "line-changed", payload: { code: "PM", fields: ["variantId", "pricingMode"] } }),
    ]);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("Details changed: Description, Start date.")).toBeInTheDocument();
    expect(screen.getByText("Billing line PM changed: Product variant, Pricing rule.")).toBeInTheDocument();
  });

  it("reads a role change as the roles it went between", async () => {
    stubTimeline([
      entry({
        id: 7,
        eventType: "role-changed",
        payload: { userId: "2", displayName: "Alan Turing", oldRole: "member", newRole: "manager" },
      }),
    ]);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("Alan Turing changed from Member to Manager.")).toBeInTheDocument();
  });

  it("reads each milestone event as the sentence it stands for", async () => {
    stubTimeline([
      entry({ id: 10, eventType: "milestone-added", payload: { milestoneId: 1, name: "Kick-off" } }),
      entry({ id: 11, eventType: "milestone-removed", payload: { milestoneId: 1, name: "Kick-off" } }),
      entry({ id: 12, eventType: "milestone-ready", payload: { milestoneId: 2, name: "Launch" } }),
      entry({ id: 13, eventType: "milestone-planned", payload: { milestoneId: 2, name: "Launch" } }),
      entry({ id: 14, eventType: "milestone-invoiced", payload: { milestoneId: 2, name: "Launch" } }),
      entry({ id: 15, eventType: "milestone-cancelled", payload: { milestoneId: 3, name: "Handover" } }),
      entry({ id: 16, eventType: "milestone-reopened", payload: { milestoneId: 3, name: "Handover" } }),
    ]);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("Billing milestone Kick-off was added.")).toBeInTheDocument();
    expect(screen.getByText("Billing milestone Kick-off was deleted.")).toBeInTheDocument();
    expect(screen.getByText("Billing milestone Launch is ready to invoice.")).toBeInTheDocument();
    expect(screen.getByText("Billing milestone Launch went back to planned.")).toBeInTheDocument();
    expect(screen.getByText("Billing milestone Launch was marked invoiced.")).toBeInTheDocument();
    expect(screen.getByText("Billing milestone Handover was cancelled.")).toBeInTheDocument();
    expect(screen.getByText("Billing milestone Handover was reopened.")).toBeInTheDocument();
  });

  it("names the milestone fields an edit moved, in the reader's own words", async () => {
    stubTimeline([
      entry({
        id: 17,
        eventType: "milestone-changed",
        payload: { milestoneId: 1, name: "Kick-off", fields: ["plannedDate", "amount", "percent"] },
      }),
    ]);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(
      await screen.findByText("Billing milestone Kick-off changed: Planned date, Amount, Percent."),
    ).toBeInTheDocument();
  });

  it("says when undoing an invoicing turned a share into a flat amount", async () => {
    stubTimeline([
      entry({ id: 18, eventType: "milestone-invoice-undone", payload: { milestoneId: 1, name: "Kick-off" } }),
      entry({
        id: 19,
        eventType: "milestone-invoice-undone",
        payload: { milestoneId: 2, name: "Launch", convertedToAmount: true },
      }),
    ]);
    renderWithProviders(<ProjectTimeline projectId={7} />);

    expect(await screen.findByText("The invoicing of billing milestone Kick-off was undone.")).toBeInTheDocument();
    expect(
      screen.getByText("The invoicing of billing milestone Launch was undone, and it now carries a flat amount."),
    ).toBeInTheDocument();
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
