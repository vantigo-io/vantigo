import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { mergeCustomer } from "./merge";
import { ApiConflictError } from "./request";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

// Literally the body the server sends: the survivor carries no owner, group,
// tags or mergedInto keys at all, because it omits what is unset.
const answered = {
  customer: {
    id: 1002,
    customerNumber: 2,
    name: "Acme AS",
    status: "active",
    type: "business",
    createdAt: "2026-06-01T10:00:00Z",
    updatedAt: "2026-09-24T10:00:00Z",
    timelineSummary: { entryCount: 17, latestOccurredOn: "2026-09-24" },
    revision: 5,
  },
  moved: [
    { kind: "customers.contacts", count: 3 },
    { kind: "projects.projects", count: 2 },
  ],
};

describe("mergeCustomer", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("POSTs the source and this customer's revision, and normalises the survivor it answers", async () => {
    const fetchMock = vi.fn<(url: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(() =>
      Promise.resolve(jsonResponse(200, answered)),
    );
    stubFetch(fetchMock);

    const result = await mergeCustomer(1002, { sourceId: 1005, revision: 4 });

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1002/merge", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sourceId: 1005, revision: 4 }),
    });
    expect(result.customer).toMatchObject({
      id: 1002,
      revision: 5,
      owner: null,
      group: null,
      mergedInto: null,
      anonymisation: null,
      tags: [],
    });
    expect(result.moved).toEqual(answered.moved);
  });

  it("throws a refusal as an ApiConflictError carrying its code", async () => {
    stubFetch(
      vi.fn(() =>
        Promise.resolve(
          jsonResponse(409, {
            title: "Customer already merged",
            status: 409,
            code: "merge_already_merged",
            detail: "#5 Acme Norge AS was already merged into #2 Acme AS.",
          }),
        ),
      ),
    );

    const error = await mergeCustomer(1003, { sourceId: 1005 }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiConflictError);
    expect((error as ApiConflictError).code).toBe("merge_already_merged");
  });
});
