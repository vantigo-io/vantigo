import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TimelineEntry } from "../api/timeline";
import { createTimelineEntry, deleteTimelineEntry, fetchTimeline, updateTimelineEntry } from "../api/timeline";
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
        <CustomerTimeline customerId={42} />
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
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ data: [entry()], nextCursor: null }))
      .mockResolvedValueOnce(json(entry({ note: "User changed this note" })))
      .mockResolvedValueOnce(json({ data: [entry({ note: "User changed this note" })], nextCursor: null }));
    await renderTimeline(fetchMock);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Edit"));
    const description = await screen.findByRole("textbox", { name: "Description" });
    await userEvent.clear(description);
    await userEvent.type(description, "User changed this note");
    expect(description).toHaveValue("User changed this note");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    const [url, init] = fetchMock.mock.calls[1];
    expect(url).toBe("/api/v1/customers/42/timeline/7");
    expect(init.method).toBe("PUT");
    expect(init.headers).toEqual({ "Content-Type": "application/json" });
    expect(JSON.parse(init.body)).toMatchObject({
      eventType: "note",
      note: "User changed this note",
      expectedRevision: 2,
    });
  });

  it("submits a selected manual event through the create form", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ data: [], nextCursor: null }))
      .mockResolvedValueOnce(json(entry({ eventType: "interaction.meeting", note: "Meet" })))
      .mockResolvedValueOnce(
        json({ data: [entry({ eventType: "interaction.meeting", note: "Meet" })], nextCursor: null }),
      );
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
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    const [url, init] = fetchMock.mock.calls[1];
    expect(url).toBe("/api/v1/customers/42/timeline");
    expect(init.method).toBe("POST");
    expect(init.headers).toEqual({ "Content-Type": "application/json" });
    expect(JSON.parse(init.body)).toMatchObject({ eventType: "interaction.meeting", note: "Meet" });
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
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ data: [entry()], nextCursor: null }))
      .mockResolvedValueOnce(new Response(null, { status: 409 }))
      .mockResolvedValueOnce(json({ data: [entry({ note: "Latest" })], nextCursor: null }))
      .mockResolvedValueOnce(json({ data: [entry({ note: "Latest" })], nextCursor: null }));
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
