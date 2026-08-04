import { describe, expect, it, vi } from "vitest";
import { messageEventsQueryOptions, messagesQueryOptions } from "./messages";

describe("communications message API mapping", () => {
  it("requests the Customer-style paginated path", async () => {
    const response = {
      data: [],
      pagination: { page: 3, pageSize: 10, totalCount: 0, totalPages: 0, hasNextPage: false, hasPreviousPage: true },
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(response), { status: 200 })));
    const options = messagesQueryOptions(3, 10);
    const result = await options.queryFn!({ signal: new AbortController().signal } as never);
    expect(fetch).toHaveBeenCalledWith(
      "/api/v1/messages?page=3&pageSize=10",
      expect.objectContaining({ credentials: "include" }),
    );
    expect(result).toEqual(response);
  });

  it("requests paginated events", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: [], pagination: {} }), { status: 200 })),
    );
    await messageEventsQueryOptions("m-7", 2, 10).queryFn!({ signal: new AbortController().signal } as never);
    expect(fetch).toHaveBeenCalledWith(
      "/api/v1/messages/m-7/events?page=2&pageSize=10",
      expect.objectContaining({ credentials: "include" }),
    );
  });
});
