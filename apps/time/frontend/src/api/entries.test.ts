import { describe, expect, it } from "vitest";
import { jsonResponse, runQuery, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { entry } from "../test/fixtures";
import {
  createTimeEntry,
  deleteTimeEntry,
  submitTimeEntries,
  timeEntriesQueryOptions,
  timeEntryQueryOptions,
  timeEntryUpdateFrom,
  updateTimeEntry,
} from "./entries";

const emptyPage = { data: [], pagination: { page: 1, pageSize: 25, totalCount: 0, totalPages: 0 } };

describe("timeEntriesQueryOptions", () => {
  it("asks for only the filters it was given, under the time key root", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, emptyPage)));

    const options = timeEntriesQueryOptions({ weekStart: "2026-09-14", projectId: 1001, status: "draft", page: 2 });
    await runQuery(options);

    expect(options.queryKey[0]).toBe("time");
    expect(fetchMock.actualCalls[0]?.[0]).toBe(
      "/api/v1/time/entries?weekStart=2026-09-14&projectId=1001&status=draft&page=2",
    );
  });

  it("lists without a query string when nothing narrows it", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, emptyPage)));

    await runQuery(timeEntriesQueryOptions({}));

    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/entries");
  });

  it("carries a user and a page size", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, emptyPage)));

    await runQuery(timeEntriesQueryOptions({ userId: "u-1", pageSize: 100 }));

    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/entries?userId=u-1&pageSize=100");
  });
});

describe("timeEntryQueryOptions", () => {
  it("reads one entry by id", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, entry())));

    const options = timeEntryQueryOptions(501);
    await runQuery(options);

    expect(options.queryKey).toEqual(["time", "entries", "detail", 501]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/entries/501");
  });
});

describe("entry writes", () => {
  it("creates an entry with the body it was given", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, entry())));

    await createTimeEntry({ projectId: 1001, entryDate: "2026-09-14", hours: 7.5, billingLineId: 3001 });

    expect(sent(fetchMock, "POST")).toEqual({
      url: "/api/v1/time/entries",
      body: { projectId: 1001, entryDate: "2026-09-14", hours: 7.5, billingLineId: 3001 },
    });
  });

  it("replaces an entry with a PUT carrying the revision", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, entry())));

    await updateTimeEntry(501, { projectId: 1001, entryDate: "2026-09-14", hours: 4, revision: 2 });

    expect(sent(fetchMock, "PUT")).toEqual({
      url: "/api/v1/time/entries/501",
      body: { projectId: 1001, entryDate: "2026-09-14", hours: 4, revision: 2 },
    });
  });

  it("deletes an entry", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(new Response(null, { status: 204 })));

    await deleteTimeEntry(501);

    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/entries/501");
    expect(fetchMock.actualCalls[0]?.[1]?.method).toBe("DELETE");
  });

  it("submits single entries by id", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    await submitTimeEntries([501, 502]);

    expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/time/entries/submit", body: { ids: [501, 502] } });
  });
});

describe("timeEntryUpdateFrom", () => {
  it("carries every field of the entry as it stands, with the change applied", () => {
    const current = entry({ taskId: 5001, note: "Kickoff", startTime: "08:00", endTime: "15:30", billable: false });

    expect(timeEntryUpdateFrom(current, { note: "Kickoff meeting" })).toEqual({
      projectId: 1001,
      billingLineId: 3001,
      taskId: 5001,
      entryDate: "2026-09-14",
      hours: 7.5,
      startTime: "08:00",
      endTime: "15:30",
      note: "Kickoff meeting",
      billable: false,
      revision: 2,
    });
  });
});
