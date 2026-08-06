import { describe, expect, it, vi } from "vitest";
import { createMailbox, mailboxesQueryOptions, updateMailbox } from "./mailboxes";

describe("mailbox API mapping", () => {
  it("lists mailboxes", async () => {
    const response = [
      { id: "box-1", fromAddress: "mail@example.com", displayName: null, createdAt: "2026-01-01", isActive: true },
    ];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(response), { status: 200 })));
    const queryFn = mailboxesQueryOptions().queryFn;
    if (!queryFn) throw new Error("mailbox query function is required");
    await expect(queryFn({ signal: new AbortController().signal } as never)).resolves.toEqual(response);
    expect(fetch).toHaveBeenCalledWith("/api/v1/mailboxes", expect.objectContaining({ credentials: "include" }));
  });

  it("creates and updates a mailbox", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(new Response(JSON.stringify({ token: "csrf-mailbox" }), { status: 200 }))
        .mockResolvedValueOnce(new Response(JSON.stringify({ id: "box-1" }), { status: 201 }))
        .mockResolvedValueOnce(new Response(JSON.stringify({ id: "box-1", isActive: false }), { status: 200 })),
    );
    await createMailbox({ fromAddress: "mail@example.com" });
    await updateMailbox("box-1", { displayName: null, isActive: false });
    expect(fetch).toHaveBeenNthCalledWith(2, "/api/v1/mailboxes", expect.objectContaining({ method: "POST" }));
    expect(fetch).toHaveBeenNthCalledWith(3, "/api/v1/mailboxes/box-1", expect.objectContaining({ method: "PUT" }));
  });
});
