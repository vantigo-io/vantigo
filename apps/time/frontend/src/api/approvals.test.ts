import { describe, expect, it } from "vitest";
import { jsonResponse, runQuery, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { approvalsQueryOptions, approveTimeEntries, rejectTimeEntries, unapproveTimeEntries } from "./approvals";

describe("approvalsQueryOptions", () => {
  it("pages the approval queue", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(jsonResponse(200, { data: [], pagination: { page: 2, pageSize: 25 } })),
    );

    const options = approvalsQueryOptions(2);
    await runQuery(options);

    expect(options.queryKey).toEqual(["time", "approvals", 2]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/approvals?page=2&pageSize=25");
  });
});

describe("approval writes", () => {
  it("approves by id", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));
    await approveTimeEntries([1, 2]);
    expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/time/entries/approve", body: { ids: [1, 2] } });
  });

  it("rejects by id with the reason", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));
    await rejectTimeEntries([3], "Wrong line");
    expect(sent(fetchMock, "POST")).toEqual({
      url: "/api/v1/time/entries/reject",
      body: { ids: [3], reason: "Wrong line" },
    });
  });

  it("unapproves by id", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));
    await unapproveTimeEntries([4]);
    expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/time/entries/unapprove", body: { ids: [4] } });
  });
});
