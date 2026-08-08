import { describe, expect, it, vi } from "vitest";
import {
  archiveMessage,
  createMessage,
  messageEventsQueryOptions,
  messagesQueryOptions,
  resendMessage,
  unarchiveMessage,
} from "./messages";
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

  it("includes archived messages when requested", async () => {
    const response = {
      data: [],
      pagination: { page: 1, pageSize: 20, totalCount: 0, totalPages: 0, hasNextPage: false, hasPreviousPage: false },
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(response), { status: 200 })));
    const queryFn = messagesQueryOptions(1, 20, undefined, true).queryFn;
    if (!queryFn) throw new Error("messages query function is required");
    await queryFn({ signal: new AbortController().signal } as never);
    expect(fetch).toHaveBeenCalledWith(
      "/api/v1/messages?page=1&pageSize=20&includeArchived=true",
      expect.objectContaining({ credentials: "include" }),
    );
  });

  it("resends a message with the chosen scope", async () => {
    clearCsrfToken();
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(new Response(JSON.stringify({ token: "csrf-token-3" }), { status: 200 }))
        .mockResolvedValueOnce(
          new Response(
            JSON.stringify({ messageId: "m-10", status: "queued", scope: "failed", requeuedRecipientCount: 2 }),
            { status: 200 },
          ),
        ),
    );
    const result = await resendMessage("m-10", "failed");
    expect(result.requeuedRecipientCount).toBe(2);
    const call = vi.mocked(fetch).mock.calls.at(-1);
    expect(call?.[0]).toBe("/api/v1/messages/m-10/resend");
    expect(call?.[1]).toEqual(expect.objectContaining({ method: "POST", body: JSON.stringify({ scope: "failed" }) }));
    expect(new Headers(call?.[1]?.headers).get("X-XSRF-TOKEN")).toBe("csrf-token-3");
  });

  it("archives and unarchives a message", async () => {
    clearCsrfToken();
    const detail = { id: "m-11", archivedAt: "2026-01-01T00:00:00Z" };
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(new Response(JSON.stringify({ token: "csrf-token-4" }), { status: 200 }))
        .mockResolvedValueOnce(new Response(JSON.stringify(detail), { status: 200 }))
        .mockResolvedValueOnce(new Response(JSON.stringify({ ...detail, archivedAt: null }), { status: 200 })),
    );
    const archived = await archiveMessage("m-11");
    expect(archived.archivedAt).toBe("2026-01-01T00:00:00Z");
    expect(vi.mocked(fetch).mock.calls.at(-1)?.[0]).toBe("/api/v1/messages/m-11/archive");
    const restored = await unarchiveMessage("m-11");
    expect(restored.archivedAt).toBeNull();
    expect(vi.mocked(fetch).mock.calls.at(-1)?.[0]).toBe("/api/v1/messages/m-11/unarchive");
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
