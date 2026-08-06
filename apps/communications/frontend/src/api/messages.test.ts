import { describe, expect, it, vi } from "vitest";
import { createMessage, messageEventsQueryOptions, messagesQueryOptions } from "./messages";
import { clearCsrfToken } from "./request";

describe("communications message API mapping", () => {
  it("requests the Customer-style paginated path", async () => {
    const response = {
      data: [],
      pagination: { page: 3, pageSize: 10, totalCount: 0, totalPages: 0, hasNextPage: false, hasPreviousPage: true },
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(response), { status: 200 })));
    const options = messagesQueryOptions(3, 10);
    const queryFn = options.queryFn;
    expect(queryFn).toBeDefined();
    if (!queryFn) throw new Error("messages query function is required");
    const result = await queryFn({ signal: new AbortController().signal } as never);
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
    const queryFn = messageEventsQueryOptions("m-7", 2, 10).queryFn;
    expect(queryFn).toBeDefined();
    if (!queryFn) throw new Error("message events query function is required");
    await queryFn({ signal: new AbortController().signal } as never);
    expect(fetch).toHaveBeenCalledWith(
      "/api/v1/messages/m-7/events?page=2&pageSize=10",
      expect.objectContaining({ credentials: "include" }),
    );
  });

  it("creates a message with the idempotency key", async () => {
    clearCsrfToken();
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(new Response(JSON.stringify({ token: "csrf-token" }), { status: 200 }))
        .mockResolvedValueOnce(
          new Response(JSON.stringify({ messageId: "m-8", status: "queued", idempotencyKey: "key-8" }), {
            status: 201,
          }),
        ),
    );
    const result = await createMessage({ subject: "Hello", to: [{ email: "person@example.com" }] }, "key-8");
    expect(result.messageId).toBe("m-8");
    const call = vi.mocked(fetch).mock.calls.at(-1);
    expect(call?.[0]).toBe("/api/v1/messages");
    const init = call?.[1];
    expect(init).toEqual(expect.objectContaining({ method: "POST", credentials: "include" }));
    expect(new Headers(init?.headers).get("Idempotency-Key")).toBe("key-8");
    expect(new Headers(init?.headers).get("X-XSRF-TOKEN")).toBe("csrf-token");
  });

  it("maps structured API errors", async () => {
    clearCsrfToken();
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(new Response(JSON.stringify({ token: "csrf-token-2" }), { status: 200 }))
        .mockResolvedValueOnce(
          new Response(
            JSON.stringify({
              error: {
                code: "recipient_suppressed",
                message: "Recipient suppressed",
                fields: { recipients: ["person@example.com"] },
              },
            }),
            { status: 422 },
          ),
        ),
    );
    await expect(
      createMessage({ subject: "Hello", to: [{ email: "person@example.com" }] }, "key-9"),
    ).rejects.toMatchObject({
      status: 422,
      code: "recipient_suppressed",
      fields: { recipients: ["person@example.com"] },
    });
  });
});
