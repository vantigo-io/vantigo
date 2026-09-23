import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TimelineEntry } from "../api/timeline";
import {
  createTimelineEntry,
  deleteTimelineEntry,
  fetchTimeline,
  normalizeFollowUp,
  normalizeTimelineEntry,
  updateTimelineEntry,
} from "../api/timeline";
import { stubFetch } from "../test/fetch";
import { CustomerTimeline } from "./-customer-timeline";

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

import { notifications } from "@mantine/notifications";

const entry = (overrides: Partial<TimelineEntry> = {}): TimelineEntry => ({
  id: 7,
  provenance: "manual",
  eventType: "note",
  producer: "",
  occurredOn: "2020-07-20",
  occurredAt: null,
  note: "Original note",
  summary: null,
  sourceUrl: null,
  payload: null,
  currentRevision: 2,
  createdAt: "2026-07-20T00:00:00Z",
  updatedAt: "2026-07-20T00:00:00Z",
  actorKind: "user",
  followUp: null,
  ...overrides,
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const renderTimeline = async (fetchMock: ReturnType<typeof vi.fn>) => {
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <CustomerTimeline customerId={42} canManageTimeline />
      </QueryClientProvider>
    </MantineProvider>,
  );
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  return queryClient;
};
const actionsButton = () => screen.getByRole("button", { name: /actions for note/i });

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("timeline API contract", () => {
  it("invalidates every filtered timeline cache through the non-exact customer prefix", async () => {
    const queryClient = new QueryClient();
    const refetch = vi.fn();
    const manualKey = [
      "customers",
      42,
      "timeline",
      { provenance: "manual", eventTypes: ["note"], occurredFrom: null, occurredTo: null },
    ];
    const generatedKey = [
      "customers",
      42,
      "timeline",
      { provenance: "generated", eventTypes: [], occurredFrom: null, occurredTo: null },
    ];
    queryClient.setQueryData(manualKey, { pages: [{ data: [], nextCursor: null }], pageParams: [undefined] });
    queryClient.setQueryData(generatedKey, { pages: [{ data: [], nextCursor: null }], pageParams: [undefined] });
    queryClient
      .getQueryCache()
      .find({ queryKey: manualKey })
      ?.setState({
        data: { pages: [{ data: [], nextCursor: null }], pageParams: [undefined] },
        dataUpdateCount: 0,
        dataUpdatedAt: Date.now(),
        error: null,
        errorUpdateCount: 0,
        errorUpdatedAt: 0,
        fetchFailureCount: 0,
        fetchFailureReason: null,
        fetchMeta: null,
        isInvalidated: false,
        status: "success",
        fetchStatus: "idle",
      });
    await queryClient.invalidateQueries({ queryKey: ["customers", 42, "timeline"] });
    expect(queryClient.getQueryCache().find({ queryKey: manualKey })?.state.isInvalidated).toBe(true);
    expect(queryClient.getQueryCache().find({ queryKey: generatedKey })?.state.isInvalidated).toBe(true);
    expect(refetch).not.toHaveBeenCalled();
  });
  it("sends canonical event types in POST and PUT payloads", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json(entry({ eventType: "interaction.meeting" })))
      .mockResolvedValueOnce(json(entry({ eventType: "interaction.email" })));
    stubFetch(fetchMock);

    await createTimelineEntry(42, { eventType: "interaction.meeting", occurredOn: "2026-07-20", note: "Meet" });
    await updateTimelineEntry(42, 7, { eventType: "interaction.email", occurredOn: "2026-07-21", note: "Email" }, 2);

    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({
      eventType: "interaction.meeting",
      occurredOn: "2026-07-20",
      note: "Meet",
    });
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({
      eventType: "interaction.email",
      occurredOn: "2026-07-21",
      note: "Email",
      expectedRevision: 2,
    });
  });

  it("reads cursor pages and accepts a 204 delete", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ data: [entry()], nextCursor: "next-1" }))
      .mockResolvedValueOnce(json({ data: [], nextCursor: null }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    stubFetch(fetchMock);

    expect((await fetchTimeline(42)).nextCursor).toBe("next-1");
    await fetchTimeline(42, "next-1");
    await deleteTimelineEntry(42, entry());
    expect(fetchMock.mock.calls[1][0]).toContain("cursor=next-1");
    const deleteCall = fetchMock.mock.calls.find(([url]) => String(url).includes("expectedRevision=2"));
    expect(deleteCall?.[1].method).toBe("DELETE");
  });

  // Every UI-level fixture below sets `assignee` and `doneAt` explicitly (even
  // to null), because that is what an OPEN, ASSIGNED follow-up looks like on
  // the wire. It never exercises the absent case the normaliser exists for:
  // the server omits `followUp` entirely when an entry carries none, and omits
  // `assignee`/`doneAt` within it when unassigned/open (api-schema.d.ts:
  // TimelineFollowUp, TimelineResponse). This is the one place that shape is
  // sent in, literally as the server sends it.
  it("normalizes the wire's absent follow-up keys to null", () => {
    expect(normalizeFollowUp(undefined)).toBeNull();
    expect(normalizeFollowUp(null)).toBeNull();
    expect(normalizeFollowUp({ dueOn: "2026-08-01" })).toEqual({ dueOn: "2026-08-01", assignee: null, doneAt: null });
    expect(normalizeTimelineEntry({ ...entry(), followUp: undefined }).followUp).toBeNull();
  });

  // The helper above proves the normaliser; this proves `fetchTimeline` is
  // actually wired through it, which is what every reader of `entry.followUp`
  // relies on. Without it the two could drift: a page read that skipped the
  // normaliser would hand components `undefined` and every `followUp &&` guard
  // would silently stop rendering instead of failing.
  it("normalizes an omitted follow-up on the way out of fetchTimeline", async () => {
    const wire: Record<string, unknown> = { ...entry() };
    delete wire.followUp;
    stubFetch(vi.fn().mockResolvedValue(json({ data: [wire], nextCursor: null })));

    const page = await fetchTimeline(42);
    expect(page.data[0].followUp).toBeNull();
  });
});

describe("CustomerTimeline", () => {
  it("renders generated entries without edit or delete actions", async () => {
    await renderTimeline(
      vi.fn().mockResolvedValue(
        json({
          data: [
            entry({
              provenance: "generated",
              eventType: "customer.contact_attached",
              note: null,
              summary: "Linked from customer relationship",
            }),
          ],
          nextCursor: null,
        }),
      ),
    );
    expect(await screen.findByText("Contact linked")).toBeInTheDocument();
    expect(screen.getByText("Linked from customer relationship")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /actions for contact linked/i })).not.toBeInTheDocument();
    expect(screen.queryByText("Edit")).not.toBeInTheDocument();
  });

  it("shows the entry's author next to its date", async () => {
    await renderTimeline(
      vi.fn().mockResolvedValue(json({ data: [entry({ actorDisplay: "Anders Refsdal" })], nextCursor: null })),
    );
    expect(await screen.findByText(/Anders Refsdal/)).toBeInTheDocument();
  });

  it("falls back to Unattributed when the entry has no author", async () => {
    await renderTimeline(
      vi
        .fn()
        .mockResolvedValue(
          json({ data: [entry({ actorKind: "unattributed", actorDisplay: "Unattributed" })], nextCursor: null }),
        ),
    );
    expect(await screen.findByText(/Unattributed/)).toBeInTheDocument();
  });

  it("labels a generated event's author from its kind, not from the name the server snapshotted", async () => {
    // The server stores the English literal "System" for a generated event, and
    // the English catalogue says "System" too — so the fixture's actorDisplay
    // has to disagree for an English-locale test to see which of the two the
    // card actually reads. It is the kind: that is what lets the Norwegian
    // catalogue translate these sentinels instead of leaking them
    // (lib/actor-label.ts, and its own test for the mapping itself).
    await renderTimeline(
      vi
        .fn()
        .mockResolvedValue(
          json({ data: [entry({ actorKind: "system", actorDisplay: "not the label" })], nextCursor: null }),
        ),
    );
    expect(await screen.findByText(/System/)).toBeInTheDocument();
    expect(screen.queryByText(/not the label/)).not.toBeInTheDocument();
  });

  it("loads the next page using the returned cursor", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ data: [entry()], nextCursor: "cursor-2" }))
      .mockResolvedValueOnce(json({ data: [entry({ id: 8, note: "Second page" })], nextCursor: null }));
    await renderTimeline(fetchMock);
    await userEvent.click(await screen.findByRole("button", { name: "Load more" }));
    expect(await screen.findByText("Second page")).toBeInTheDocument();
    expect(fetchMock.mock.calls[1][0]).toContain("cursor=cursor-2");
  });

  it("keeps a changed edit value while the form remains open", async () => {
    // URL/method dispatch rather than a positional queue: opening the edit
    // form now also mounts the follow-up assignee's UserPicker, which fires
    // its own GET as soon as the form opens — a positional queue would hand
    // that call the response meant for the PUT (see global-constraints: never
    // assert "the last fetch", match by URL/method instead).
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/assignable-users")) return Promise.resolve(json([]));
      if (init?.method === "PUT") return Promise.resolve(json(entry({ note: "User changed this note" })));
      return Promise.resolve(json({ data: [entry()], nextCursor: null }));
    });
    await renderTimeline(fetchMock);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Edit"));
    const description = await screen.findByRole("textbox", { name: "Description" });
    await userEvent.clear(description);
    await userEvent.type(description, "User changed this note");
    expect(description).toHaveValue("User changed this note");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => {
      const putCall = fetchMock.mock.calls.find(
        ([callUrl, callInit]) => String(callUrl) === "/api/v1/customers/42/timeline/7" && callInit?.method === "PUT",
      );
      expect(putCall).toBeDefined();
    });
    // Matching by URL and method says a PUT happened; counting the matches is
    // the property that says it happened ONCE — the thing a `find` on its own
    // would let a double submit through.
    const putCalls = fetchMock.mock.calls.filter(
      ([callUrl, callInit]) => String(callUrl) === "/api/v1/customers/42/timeline/7" && callInit?.method === "PUT",
    );
    expect(putCalls).toHaveLength(1);
    const [, putInit] = putCalls[0] ?? [];
    expect(putInit?.headers).toEqual({ "Content-Type": "application/json" });
    expect(JSON.parse(String(putInit?.body))).toMatchObject({
      eventType: "note",
      note: "User changed this note",
      expectedRevision: 2,
    });
  });

  it("submits a selected manual event through the create form", async () => {
    // Same reason as the edit-form test above: the create form's own
    // UserPicker fires a GET as soon as it opens, so the mock dispatches by
    // URL/method rather than by position.
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/assignable-users")) return Promise.resolve(json([]));
      if (url.endsWith("/api/v1/customers/42/timeline") && init?.method === "POST")
        return Promise.resolve(json(entry({ eventType: "interaction.meeting", note: "Meet" })));
      return Promise.resolve(json({ data: [], nextCursor: null }));
    });
    await renderTimeline(fetchMock);
    await userEvent.click(screen.getByRole("button", { name: "Add event" }));
    const createDialog = await screen.findByRole("dialog", { name: "Add timeline event" });
    await userEvent.click(within(createDialog).getByRole("combobox", { name: "Type" }));
    const meetingOptions = await screen.findAllByText("Meeting");
    expect(meetingOptions.length).toBeGreaterThan(0);
    const meetingOption = meetingOptions.at(-1);
    expect(meetingOption).toBeDefined();
    if (!meetingOption) throw new Error("Meeting option is required");
    await userEvent.click(meetingOption);
    await userEvent.type(within(createDialog).getByRole("textbox", { name: "Description" }), "Meet");
    await userEvent.click(within(createDialog).getByRole("button", { name: /^Add event$/ }));
    await waitFor(() => {
      const postCall = fetchMock.mock.calls.find(
        ([callUrl, callInit]) => String(callUrl) === "/api/v1/customers/42/timeline" && callInit?.method === "POST",
      );
      expect(postCall).toBeDefined();
    });
    // Counted, not just found: one click is one create (same reason as the edit
    // test above).
    const postCalls = fetchMock.mock.calls.filter(
      ([callUrl, callInit]) => String(callUrl) === "/api/v1/customers/42/timeline" && callInit?.method === "POST",
    );
    expect(postCalls).toHaveLength(1);
    const [, postInit] = postCalls[0] ?? [];
    expect(postInit?.headers).toEqual({ "Content-Type": "application/json" });
    expect(JSON.parse(String(postInit?.body))).toMatchObject({ eventType: "interaction.meeting", note: "Meet" });
  });

  it("renders revisions returned inside the data envelope", async () => {
    const fetchMock = vi.fn((url: string) =>
      url.includes("/revisions")
        ? Promise.resolve(
            json({
              data: [
                {
                  revision: 2,
                  action: "update",
                  eventType: "note",
                  occurredOn: "2026-07-20",
                  occurredAt: null,
                  note: "Revised text",
                  sourceUrl: null,
                  changedAt: "2026-07-21T10:00:00Z",
                  actorKind: "unattributed",
                  actorDisplayName: "Unattributed",
                },
              ],
            }),
          )
        : Promise.resolve(json({ data: [entry()], nextCursor: null })),
    );
    await renderTimeline(fetchMock);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Revision history"));
    const dialog = await screen.findByRole("dialog", { name: "Revision history" });
    expect(await within(dialog).findByText("Revised text")).toBeInTheDocument();
    expect(within(dialog).getByText(/Unattributed/)).toBeInTheDocument();
  });

  it("refreshes after a successful 204 delete", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ data: [entry()], nextCursor: null }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(json({ data: [], nextCursor: null }));
    await renderTimeline(fetchMock);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Delete"));
    await userEvent.click(await screen.findByRole("button", { name: "Delete event" }));
    await waitFor(() => expect(screen.getByText(/No events yet/)).toBeInTheDocument());
    const deleteCall = fetchMock.mock.calls.find(([, init]) => init?.method === "DELETE");
    expect(deleteCall?.[1].method).toBe("DELETE");
    expect(notifications.show).not.toHaveBeenCalled();
  });

  it("refreshes and provides recovery feedback for edit conflicts", async () => {
    // Same reason as the two tests above: URL/method dispatch, not a
    // positional queue, now that the edit form's UserPicker fires its own GET.
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/assignable-users")) return Promise.resolve(json([]));
      if (init?.method === "PUT") return Promise.resolve(new Response(null, { status: 409 }));
      if (url.includes("/timeline?"))
        return Promise.resolve(json({ data: [entry({ note: "Latest" })], nextCursor: null }));
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Edit"));
    await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(expect.objectContaining({ title: "This event changed" })),
    );
    expect(fetchMock.mock.calls.filter(([url]) => String(url).includes("/timeline?")).length).toBe(2);
  });

  it("refreshes and provides recovery feedback for a delete conflict", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ data: [entry()], nextCursor: null }))
      .mockResolvedValueOnce(new Response(null, { status: 409 }))
      .mockResolvedValueOnce(json({ data: [entry({ note: "Latest" })], nextCursor: null }));
    await renderTimeline(fetchMock);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Delete"));
    const deleteDialog = await screen.findByRole("dialog", { name: "Delete timeline event" });
    const before = fetchMock.mock.calls.length;
    await userEvent.click(within(deleteDialog).getByRole("button", { name: "Delete event" }));
    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(expect.objectContaining({ title: "Event changed" })),
    );
    expect(fetchMock.mock.calls.length).toBeGreaterThan(before);
    expect(fetchMock.mock.calls[2][0]).toContain("/timeline?");
  });

  it("shows empty and error states", async () => {
    await renderTimeline(vi.fn().mockResolvedValue(json({ data: [], nextCursor: null })));
    expect(await screen.findByText(/No events yet/)).toBeInTheDocument();
    // A separate render is intentionally used so the empty state assertion does not
    // mask the independent request error state.
    cleanup();
    await renderTimeline(vi.fn().mockRejectedValue(new Error("offline")));
    expect(await screen.findByText("Could not load the timeline.")).toBeInTheDocument();
  });

  it("serializes desktop filters, resets pagination, and preserves filters on load more", async () => {
    const fetchMock = vi.fn((url: string) =>
      Promise.resolve(
        json({
          data: [entry({ id: url.includes("cursor") ? 2 : 1 })],
          nextCursor: url.includes("cursor") ? null : "filtered-next",
        }),
      ),
    );
    await renderTimeline(fetchMock);
    await userEvent.click(screen.getByRole("radio", { name: "Manual" }));
    const eventTypes = screen.getByRole("combobox", { name: "Event types" });
    await userEvent.click(eventTypes);
    await userEvent.click(screen.getByText("Note", { selector: "span" }));
    await userEvent.click(eventTypes);
    await userEvent.click(screen.getByText("Call", { selector: "span" }));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([url]) => String(url).includes("eventType=interaction.call") && String(url).includes("eventType=note"),
        ),
      ).toBe(true),
    );
    const filteredCall = fetchMock.mock.calls.find(
      ([url]) => String(url).includes("eventType=interaction.call") && String(url).includes("eventType=note"),
    );
    expect(filteredCall).toBeDefined();
    if (!filteredCall) throw new Error("Filtered timeline request is required");
    const filteredUrl = new URL(filteredCall[0], "http://localhost");
    expect(filteredUrl.searchParams.get("provenance")).toBe("manual");
    expect(filteredUrl.searchParams.getAll("eventType")).toEqual(["interaction.call", "note"]);
    expect(filteredUrl.searchParams.get("cursor")).toBeNull();
    await userEvent.click(await screen.findByRole("button", { name: "Load more" }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([url]) => String(url).includes("cursor=filtered-next"))).toBe(true),
    );
    const nextCall = fetchMock.mock.calls.find(([url]) => String(url).includes("cursor=filtered-next"));
    expect(nextCall).toBeDefined();
    if (!nextCall) throw new Error("Next filtered timeline request is required");
    const nextUrl = new URL(nextCall[0], "http://localhost");
    expect(nextUrl.searchParams.get("cursor")).toBe("filtered-next");
    expect(nextUrl.searchParams.get("provenance")).toBe("manual");
    expect(nextUrl.searchParams.getAll("eventType")).toEqual(["interaction.call", "note"]);
    await userEvent.click(screen.getByRole("button", { name: "Reset" }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith("limit=25"))).toBe(true));
    const resetCall = fetchMock.mock.calls.find(([url]) => String(url).endsWith("limit=25"));
    expect(resetCall).toBeDefined();
    if (!resetCall) throw new Error("Reset timeline request is required");
    const resetUrl = new URL(resetCall[0], "http://localhost");
    expect(resetUrl.searchParams.get("provenance")).toBeNull();
    expect(resetUrl.searchParams.getAll("eventType")).toEqual([]);
    expect(resetUrl.searchParams.get("cursor")).toBeNull();
  });

  it("offers the owner and tag events in the Event types filter", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(json({ data: [entry({ id: 1 })], nextCursor: null })));
    await renderTimeline(fetchMock);
    await userEvent.click(screen.getByRole("combobox", { name: "Event types" }));
    expect(screen.getByText("Owner changed", { selector: "span" })).toBeInTheDocument();
    expect(screen.getByText("Tags changed", { selector: "span" })).toBeInTheDocument();
  });

  it("labels a group move and offers it in the Event types filter", async () => {
    // The wire body a membership write produces: generated, with the snapshotted names in its summary.
    const fetchMock = vi.fn(() =>
      Promise.resolve(
        json({
          data: [
            {
              id: 3,
              provenance: "generated",
              eventType: "customer.group_changed",
              producer: "customers",
              occurredOn: "2026-09-23",
              occurredAt: "2026-09-23T10:00:00Z",
              summary: "Moved to group Retail",
              payload: { customerId: 42, before: null, after: { groupId: "g1", name: "Retail" } },
              currentRevision: 1,
              createdAt: "2026-09-23T10:00:00Z",
              updatedAt: "2026-09-23T10:00:00Z",
              actorKind: "user",
            },
          ],
        }),
      ),
    );
    await renderTimeline(fetchMock);
    expect(await screen.findByText("Group changed", { selector: "p" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("combobox", { name: "Event types" }));
    expect(screen.getByText("Group changed", { selector: "span" })).toBeInTheDocument();
  });

  it("serializes the applied date range with repeated event types", async () => {
    const fetchMock = vi.fn().mockResolvedValue(json({ data: [], nextCursor: null }));
    stubFetch(fetchMock);

    await fetchTimeline(42, undefined, undefined, {
      provenance: "manual",
      eventTypes: ["note", "interaction.call", "note"],
      occurredFrom: "2026-07-01",
      occurredTo: "2026-07-20",
    });

    const url = new URL(fetchMock.mock.calls[0][0], "http://localhost");
    expect(url.searchParams.get("provenance")).toBe("manual");
    expect(url.searchParams.getAll("eventType")).toEqual(["interaction.call", "note"]);
    expect(url.searchParams.get("occurredFrom")).toBe("2026-07-01");
    expect(url.searchParams.get("occurredTo")).toBe("2026-07-20");
  });

  it("applies and resets filters from the mobile drawer", async () => {
    vi.spyOn(window, "matchMedia").mockImplementation((query: string) => ({
      matches: query.includes("48em"),
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    const fetchMock = vi.fn().mockResolvedValue(json({ data: [], nextCursor: null }));
    await renderTimeline(fetchMock);
    await userEvent.click(screen.getByRole("button", { name: /^Filters/ }));
    const drawer = await screen.findByRole("dialog", { name: "Filters" });
    await userEvent.click(within(drawer).getByRole("radio", { name: "Automatic" }));
    await userEvent.click(within(drawer).getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(new URL(fetchMock.mock.calls[1][0], "http://localhost").searchParams.get("provenance")).toBe("generated");
    await userEvent.click(screen.getByRole("button", { name: /Filters/ }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "Filters" })).getByRole("button", { name: "Reset" }),
    );
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    expect(new URL(fetchMock.mock.calls[2][0], "http://localhost").searchParams.get("provenance")).toBeNull();
  });

  it("renders all contact payload shapes once", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      json({
        data: [
          entry({
            id: 1,
            provenance: "generated",
            note: null,
            summary: "Contact linked: Jane Doe (#1002)",
            payload: { contact: { id: 1002, displayName: "Jane Doe (#1002)" } },
          }),
          entry({
            id: 2,
            provenance: "generated",
            note: null,
            summary: null,
            payload: { displayName: "Root Person" },
          }),
          entry({
            id: 3,
            provenance: "generated",
            note: null,
            summary: null,
            payload: { contactName: "Legacy Person" },
          }),
        ],
        nextCursor: null,
      }),
    );
    await renderTimeline(fetchMock);
    expect((await screen.findAllByText(/Jane Doe \(#1002\)/)).length).toBe(1);
    expect((await screen.findAllByText(/Root Person/)).length).toBe(1);
    expect((await screen.findAllByText(/Legacy Person/)).length).toBe(1);
  });

  it.each([
    [
      "added",
      null,
      { name: "Acme", id: "123", country: "NO", type: "business", source: "brreg" },
      /Legal identity added:.*legal name added: Acme.*ID added: 123.*country added: NO.*type added: business.*source added: brreg/,
    ],
    [
      "removed",
      { name: "Acme", id: "123", country: "NO", type: "business", source: "brreg" },
      null,
      /Legal identity removed:.*legal name removed.*ID removed.*country removed.*type removed.*source removed/,
    ],
    ["name", { name: "Old", id: "123" }, { name: "New", id: "123" }, /legal name: Old → New/],
    ["id", { name: "Same", id: "123" }, { name: "Same", id: "456" }, /ID: 123 → 456/],
    [
      "other fields",
      { name: "Same", id: "123", country: "NO", type: "business", source: "manual" },
      { name: "Same", id: "123", country: "SE", type: "person", source: "brreg" },
      /country: NO → SE.*type: business → person.*source: manual → brreg/,
    ],
  ])("renders legal identity %s changes without raw values", async (_label, before, after, expected) => {
    await renderTimeline(
      vi.fn().mockResolvedValue(
        json({
          data: [
            entry({
              provenance: "generated",
              note: null,
              summary: "Customer updated",
              payload: { changes: { legalIdentity: { before, after } } },
            }),
          ],
          nextCursor: null,
        }),
      ),
    );
    expect(await screen.findByText(expected)).toBeInTheDocument();
    expect(screen.queryByText(/undefined|none|\[object Object\]/i)).not.toBeInTheDocument();
  });

  it("keeps UTC time labeling and unique action names, and suppresses duplicate delete clicks", async () => {
    vi.spyOn(window, "matchMedia").mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    const first = entry({ id: 7, occurredOn: "2026-07-20", occurredAt: "2026-07-20T23:30:00Z" });
    const second = entry({ id: 8, occurredOn: "2026-07-21", occurredAt: "2026-07-21T00:30:00Z" });
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ data: [first, second], nextCursor: null }))
      .mockImplementationOnce(() => new Promise(() => {}));
    await renderTimeline(fetchMock);
    await userEvent.click(screen.getByRole("button", { name: "Add event" }));
    const formDialog = await screen.findByRole("dialog", { name: "Add timeline event" });
    expect(within(formDialog).getByLabelText("Time (UTC)")).toBeInTheDocument();
    expect(screen.getByText(/11:30 PM UTC/)).toBeInTheDocument();
    const actions = screen.getAllByRole("button", { name: /Actions for Note on/ });
    expect(actions).toHaveLength(2);
    expect(new Set(actions.map((button) => button.getAttribute("aria-label"))).size).toBe(2);
    cleanup();
    const deleteFetch = vi
      .fn()
      .mockResolvedValueOnce(json({ data: [entry()], nextCursor: null }))
      .mockImplementationOnce(() => new Promise(() => {}));
    await renderTimeline(deleteFetch);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Delete"));
    const confirm = await screen.findByRole("button", { name: "Delete event" });
    await userEvent.click(confirm);
    await userEvent.click(confirm);
    expect(deleteFetch.mock.calls.filter(([, init]) => init?.method === "DELETE")).toHaveLength(1);
  });
});

describe("the timeline's follow-ups", () => {
  // The wire shape, literally: an entry with a follow-up, as the server sends
  // it. The boundary is what turns an absent key into null, so these fixtures
  // never carry a key the server would not.
  const withFollowUp = (followUp: unknown) => ({
    id: 7,
    provenance: "manual",
    eventType: "note",
    producer: "",
    occurredOn: "2020-07-20",
    note: "Original note",
    currentRevision: 2,
    state: "active",
    actorKind: "user",
    createdAt: "2026-07-20T00:00:00Z",
    updatedAt: "2026-07-20T00:00:00Z",
    ...(followUp === undefined ? {} : { followUp }),
  });

  it("shows an overdue follow-up with its assignee, and a done one struck through", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).includes("/timeline?")) {
        return Promise.resolve(
          json({
            data: [
              withFollowUp({
                dueOn: "2020-01-02",
                assignee: { userId: "u1", displayName: "Kari Nordmann", active: true },
                doneAt: null,
              }),
              { ...withFollowUp({ dueOn: "2020-01-03", assignee: null, doneAt: "2020-01-04T09:00:00Z" }), id: 8 },
            ],
            nextCursor: null,
          }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);

    // The open, overdue line: the "Follow up <date>" wording, the assignee's
    // name, the word that says it is late, and red.
    const openLine = await screen.findByText(/Follow up /);
    expect(openLine).toHaveTextContent(/Kari Nordmann/);
    expect(openLine).toHaveTextContent(/overdue/);
    // And red, which is `followUpTone`'s whole job. Mantine's `c` prop lands as
    // a `color` declaration naming its own variable, so that variable — not a
    // resolved colour jsdom has no stylesheet to compute — is what to assert.
    expect(openLine).toHaveStyle({ color: "var(--mantine-color-red-text)" });

    // The done line is matched by ITS OWN wording ("Followed up …"), never by
    // /done/i: the Mark done BUTTON on the other row matches that too, so a
    // /done/i assertion would pass with the done line missing entirely.
    const doneLine = screen.getByText(/Followed up /);
    expect(doneLine).not.toHaveTextContent(/overdue/);
    expect(doneLine).toHaveStyle({ textDecoration: "line-through" });
    // And it offers Reopen rather than Mark done.
    expect(screen.getByRole("button", { name: /reopen/i })).toBeInTheDocument();
  });

  // The boundary `isOverdue` gets wrong with a `<=` instead of a `<`: due
  // TODAY is still open, not yet late. Every other fixture in this describe
  // block is pinned to 2020, so only a `dueOn` computed from the clock can
  // catch that mutation.
  it("does not call a follow-up due today overdue", async () => {
    const today = new Date().toISOString().slice(0, 10);
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).includes("/timeline?")) {
        return Promise.resolve(
          json({ data: [withFollowUp({ dueOn: today, assignee: null, doneAt: null })], nextCursor: null }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);

    const openLine = await screen.findByText(/Follow up /);
    expect(openLine).not.toHaveTextContent(/overdue/);
  });

  it("marks a follow-up done through the entry's own path and refreshes the feed", async () => {
    const done = vi.fn(() =>
      Promise.resolve(json(withFollowUp({ dueOn: "2020-01-02", assignee: null, doneAt: "2020-01-05T10:00:00Z" }))),
    );
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/follow-up/done")) return done();
      if (url.includes("/timeline?")) {
        return Promise.resolve(
          json({ data: [withFollowUp({ dueOn: "2020-01-02", assignee: null, doneAt: null })], nextCursor: null }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    const stub = stubFetch(fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerTimeline customerId={42} canManageTimeline />
        </QueryClientProvider>
      </MantineProvider>,
    );
    await screen.findByRole("button", { name: /mark done/i });
    await userEvent.click(screen.getByRole("button", { name: /mark done/i }));

    // Never "the last fetch": find the call by method and URL.
    await waitFor(() => {
      const call = stub.actualCalls.find(
        ([url, init]) =>
          String(url).endsWith("/api/v1/customers/42/timeline/7/follow-up/done") && init?.method === "POST",
      );
      expect(call).toBeDefined();
    });
  });

  it("reopens a done follow-up through the same path with DELETE", async () => {
    const reopen = vi.fn(() =>
      Promise.resolve(json(withFollowUp({ dueOn: "2020-01-02", assignee: null, doneAt: null }))),
    );
    let ticked = true;
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/follow-up/done")) {
        ticked = false;
        return reopen();
      }
      if (url.includes("/timeline?")) {
        return Promise.resolve(
          json({
            data: [
              withFollowUp({ dueOn: "2020-01-02", assignee: null, doneAt: ticked ? "2020-01-04T09:00:00Z" : null }),
            ],
            nextCursor: null,
          }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    const stub = stubFetch(fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerTimeline customerId={42} canManageTimeline />
        </QueryClientProvider>
      </MantineProvider>,
    );
    await userEvent.click(await screen.findByRole("button", { name: /reopen/i }));

    await waitFor(() => {
      const call = stub.actualCalls.find(
        ([url, init]) =>
          String(url).endsWith("/api/v1/customers/42/timeline/7/follow-up/done") && init?.method === "DELETE",
      );
      expect(call).toBeDefined();
    });
    // The refresh re-reads, and the line is open again: "Follow up …", no strike.
    expect(await screen.findByText(/Follow up /)).toBeInTheDocument();
    expect(screen.queryByText(/Followed up /)).not.toBeInTheDocument();
  });

  it("hides every timeline control from a reader who cannot manage the timeline", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).includes("/timeline?")) {
        return Promise.resolve(
          json({ data: [withFollowUp({ dueOn: "2020-01-02", assignee: null, doneAt: null })], nextCursor: null }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    // Rendered HERE rather than through renderTimeline, and deliberately with
    // no canManageTimeline at all: the shared helper passes it, so this is the
    // one place the withheld case is exercised.
    stubFetch(fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerTimeline customerId={42} />
        </QueryClientProvider>
      </MantineProvider>,
    );

    await screen.findByText("Original note");
    expect(screen.queryByRole("button", { name: /add event/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /mark done/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /actions for note/i })).not.toBeInTheDocument();
    // The follow-up itself is still readable — only the control is gone.
    expect(screen.getByText(/Follow up /)).toBeInTheDocument();
  });

  it("shows each revision's own follow-up, so a ticked revision names who ticked it and when", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/revisions")) {
        return Promise.resolve(
          json({
            data: [
              {
                revision: 1,
                action: "create",
                eventType: "note",
                occurredOn: "2026-07-20",
                occurredAt: null,
                note: "Original note",
                sourceUrl: null,
                changedAt: "2026-07-20T08:00:00Z",
                actorKind: "user",
                actorDisplayName: "Kari Nordmann",
                followUp: { dueOn: "2026-08-01" },
              },
              {
                revision: 2,
                action: "update",
                eventType: "note",
                occurredOn: "2026-07-20",
                occurredAt: null,
                note: "Original note",
                sourceUrl: null,
                changedAt: "2026-07-22T09:30:00Z",
                actorKind: "user",
                actorDisplayName: "Ola Nordmann",
                followUp: { dueOn: "2026-08-01", doneAt: "2026-07-22T09:30:00Z" },
              },
            ],
          }),
        );
      }
      if (url.includes("/timeline?")) {
        return Promise.resolve(
          json({
            data: [withFollowUp({ dueOn: "2026-08-01", assignee: null, doneAt: "2026-07-22T09:30:00Z" })],
            nextCursor: null,
          }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Revision history"));
    const dialog = await screen.findByRole("dialog", { name: "Revision history" });

    // Revision 1 carried an open follow-up; revision 2 is the one that ticked
    // it, and the panel's own actor and changed-at line beside it is what
    // answers "who ticked it and when" (follow-ups design D4) without a doneBy
    // field on the contract.
    expect(await within(dialog).findByText(/Follow up /)).toBeInTheDocument();
    const ticked = within(dialog).getByText(/Followed up /);
    // Each revision is its own Accordion.Panel, which Mantine renders as a
    // region — so the panel holding the "Followed up" line is the panel whose
    // actor line names who ticked it. (If this version emits another role,
    // read the markup once and assert on the panel element it does emit; the
    // requirement is that the two read together, not that it is a region.)
    const panel = ticked.closest("[role='region']");
    expect(panel).toHaveTextContent(/Ola Nordmann/);
    // …and not revision 1's author, which is the whole point of reading the two
    // together rather than anywhere on the page.
    expect(panel).not.toHaveTextContent(/Kari Nordmann/);
  });

  // The server answers a tick on a generated entry with 409 "Generated,
  // deleted, or voided timeline entries cannot be edited" (follow_ups.go's
  // second step), so the control on one could only ever fail. A generated entry
  // carrying a follow-up is not something today's writers produce — but the
  // reader is data, not a promise, and `canManageTimeline` alone did not say
  // anything about provenance, which is what the menu beside it has always
  // checked.
  it("offers no tick on a generated entry, even one that carries a follow-up", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).includes("/timeline?")) {
        return Promise.resolve(
          json({
            data: [
              {
                ...withFollowUp({ dueOn: "2020-01-02", assignee: null, doneAt: null }),
                provenance: "generated",
                producer: "customers",
                note: null,
                summary: "Customer updated",
              },
            ],
            nextCursor: null,
          }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);

    // Readable, like every other generated entry: only the control is withheld.
    expect(await screen.findByText(/Follow up /)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /mark done/i })).not.toBeInTheDocument();
  });

  it("sends the follow-up the form collected, and clears it when the section is emptied", async () => {
    const created = vi.fn(() =>
      Promise.resolve(json(withFollowUp({ dueOn: "2026-12-24", assignee: null, doneAt: null }))),
    );
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/assignable-users"))
        return Promise.resolve(json([{ userId: "u1", displayName: "Kari Nordmann" }]));
      if (url.endsWith("/api/v1/customers/42/timeline") && init?.method === "POST") return created();
      if (url.includes("/timeline?")) return Promise.resolve(json({ data: [], nextCursor: null }));
      return Promise.resolve(json({ data: [] }));
    });
    const stub = stubFetch(fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerTimeline customerId={42} canManageTimeline />
        </QueryClientProvider>
      </MantineProvider>,
    );
    await userEvent.click(await screen.findByRole("button", { name: /add event/i }));
    // Scoped to the dialog, like the pre-existing create-form test just above:
    // the header's own "Add event" button is still on screen (Mantine's Modal
    // does not hide it from the accessibility tree), and the create form's
    // submit button reads "Add event" too — so an unscoped query for either
    // matches both.
    const dialog = await screen.findByRole("dialog", { name: /add timeline event/i });
    await userEvent.type(within(dialog).getByRole("textbox", { name: /description/i }), "Call back");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /follow up on/i }), "2026-12-24");
    await userEvent.click(within(dialog).getByRole("button", { name: /^add event$/i }));

    await waitFor(() => {
      const call = stub.actualCalls.find(
        ([url, init]) => String(url).endsWith("/api/v1/customers/42/timeline") && init?.method === "POST",
      );
      expect(call).toBeDefined();
      expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({ followUp: { dueOn: "2026-12-24" } });
    });
  });

  // The other half of that sentence, and the one the form's own comment calls an
  // instruction: the PUT is a full replace, so an emptied date does not mean
  // "leave the follow-up alone", it means there is none any more. What proves it
  // is the key being ABSENT from the body — `undefined` is what JSON.stringify
  // drops — because absent is what the contract reads as a clear.
  it("clears a follow-up by emptying the date, and the PUT then carries no followUp at all", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/assignable-users")) return Promise.resolve(json([]));
      if (init?.method === "PUT") return Promise.resolve(json(withFollowUp(undefined)));
      if (url.includes("/timeline?")) {
        return Promise.resolve(
          json({ data: [withFollowUp({ dueOn: "2026-12-24", assignee: null, doneAt: null })], nextCursor: null }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Edit"));
    const dialog = await screen.findByRole("dialog", { name: /edit timeline event/i });
    const followUpOn = within(dialog).getByRole("textbox", { name: /follow up on/i });
    expect(followUpOn).toHaveValue("2026-12-24");
    await userEvent.clear(followUpOn);
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => {
      const put = fetchMock.mock.calls.find(
        ([url, init]) => String(url) === "/api/v1/customers/42/timeline/7" && init?.method === "PUT",
      );
      expect(put).toBeDefined();
      expect(Object.hasOwn(JSON.parse(String(put?.[1]?.body)), "followUp")).toBe(false);
    });
  });

  // The follow-up an entry already has must survive an edit of something else,
  // and the PUT is a full replace — so the form seeding `followUpAssigneeUserId`
  // from the entry is the ONLY thing standing between "fix a typo in the note"
  // and silently unassigning whoever was on it. The assignee here is one the
  // directory no longer offers (`active: false`, the disabled case the server
  // deliberately keeps), because that is the seed that cannot be recovered from
  // the picker's own option list.
  it("re-sends the follow-up's assignee and date when only the note is edited", async () => {
    const assignee = { userId: "u1", displayName: "Kari Nordmann", active: false };
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      // The directory answers ACTIVE users only, so it does not offer her: the
      // id has to come from the entry, not from anything this list returned.
      if (url.includes("/assignable-users")) return Promise.resolve(json([]));
      if (init?.method === "PUT")
        return Promise.resolve(json(withFollowUp({ dueOn: "2026-12-24", assignee, doneAt: null })));
      if (url.includes("/timeline?")) {
        return Promise.resolve(
          json({ data: [withFollowUp({ dueOn: "2026-12-24", assignee, doneAt: null })], nextCursor: null }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Edit"));
    const dialog = await screen.findByRole("dialog", { name: /edit timeline event/i });
    await userEvent.type(within(dialog).getByRole("textbox", { name: /description/i }), " — rescheduled");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => {
      const put = fetchMock.mock.calls.find(
        ([url, init]) => String(url) === "/api/v1/customers/42/timeline/7" && init?.method === "PUT",
      );
      expect(put).toBeDefined();
      expect(JSON.parse(String(put?.[1]?.body))).toMatchObject({
        note: "Original note — rescheduled",
        followUp: { dueOn: "2026-12-24", assigneeUserId: "u1" },
      });
    });
  });

  // A follow-up IS its date: there is nothing for an assignee to be on once the
  // date is gone, so emptying the date clears the assignee rather than refusing
  // the save. What it must not do is what it did before this ruling — stop at
  // "give the follow-up a date, or clear the assignee" and leave the person to
  // clear a field they did not ask about.
  it("clears the assignee with the date, and the PUT then carries no followUp at all", async () => {
    const assignee = { userId: "u1", displayName: "Kari Nordmann", active: true };
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/assignable-users")) return Promise.resolve(json([assignee]));
      if (init?.method === "PUT") return Promise.resolve(json(withFollowUp(undefined)));
      if (url.includes("/timeline?")) {
        return Promise.resolve(
          json({ data: [withFollowUp({ dueOn: "2026-12-24", assignee, doneAt: null })], nextCursor: null }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Edit"));
    const dialog = await screen.findByRole("dialog", { name: /edit timeline event/i });
    // The picker starts on her, which is what makes the clear observable.
    expect(within(dialog).getByRole("combobox", { name: /assigned to/i })).toHaveValue("Kari Nordmann");
    await userEvent.clear(within(dialog).getByRole("textbox", { name: /follow up on/i }));
    // The picker empties with the date, so the form does not go on showing a
    // person nobody is going to be sent.
    expect(within(dialog).getByRole("combobox", { name: /assigned to/i })).toHaveValue("");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => {
      const put = fetchMock.mock.calls.find(
        ([url, init]) => String(url) === "/api/v1/customers/42/timeline/7" && init?.method === "PUT",
      );
      expect(put).toBeDefined();
      expect(Object.hasOwn(JSON.parse(String(put?.[1]?.body)), "followUp")).toBe(false);
    });
  });

  it("marks an assignee the directory no longer has as inactive", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).includes("/timeline?")) {
        return Promise.resolve(
          json({
            data: [
              withFollowUp({
                dueOn: "2020-01-02",
                assignee: { userId: "u1", displayName: "Kari Nordmann", active: false },
                doneAt: null,
              }),
            ],
            nextCursor: null,
          }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);

    // Her name stays — she still holds the follow-up — with the reason it looks
    // odd said in words rather than left for the reader to wonder about.
    expect(await screen.findByText(/Kari Nordmann \(inactive\)/)).toBeInTheDocument();
  });

  // The other end of "a follow-up IS its date": with no date there is nothing for
  // an assignee to be on, so the picker is not offered rather than offered and
  // then quietly ignored by the submit mapping.
  it("offers no assignee until the follow-up has a date", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/assignable-users"))
        return Promise.resolve(json([{ userId: "u1", displayName: "Kari Nordmann" }]));
      if (url.includes("/timeline?")) return Promise.resolve(json({ data: [], nextCursor: null }));
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);
    await userEvent.click(await screen.findByRole("button", { name: /add event/i }));
    const dialog = await screen.findByRole("dialog", { name: /add timeline event/i });

    // A fresh entry starts with no follow-up date at all.
    expect(within(dialog).getByRole("combobox", { name: /assigned to/i })).toBeDisabled();
    expect(within(dialog).getByText("Give the follow-up a date first")).toBeInTheDocument();

    await userEvent.type(within(dialog).getByRole("textbox", { name: /follow up on/i }), "2026-12-24");
    expect(within(dialog).getByRole("combobox", { name: /assigned to/i })).toBeEnabled();
  });
});
