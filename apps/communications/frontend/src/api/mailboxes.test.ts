import { describe, expect, it, vi } from "vitest";
import { createMailbox, mailboxesQueryOptions, updateMailbox, verifyMailbox } from "./mailboxes";
import { clearCsrfToken } from "./request";

describe("mailbox API mapping", () => {
  it("lists mailboxes", async () => {
    const response = [
      {
        id: "box-1",
        fromAddress: "mail@example.com",
        displayName: null,
        createdAt: "2026-01-01",
        isActive: true,
        provider: "smtp",
        isDefault: true,
        hasCredentials: true,
        settings: { host: "smtp.example.com", port: 587, useSsl: true },
      },
    ];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(response), { status: 200 })));
    const queryFn = mailboxesQueryOptions().queryFn;
    if (!queryFn) throw new Error("mailbox query function is required");
    await expect(queryFn({ signal: new AbortController().signal } as never)).resolves.toEqual(response);
    expect(fetch).toHaveBeenCalledWith(
      "/api/v1/communications/mailboxes",
      expect.objectContaining({ credentials: "include" }),
    );
  });

  it("creates and updates a mailbox", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(new Response(JSON.stringify({ token: "csrf-mailbox" }), { status: 200 }))
        .mockResolvedValueOnce(new Response(JSON.stringify({ id: "box-1" }), { status: 201 }))
        .mockResolvedValueOnce(new Response(JSON.stringify({ id: "box-1", isActive: false }), { status: 200 }))
        .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 })),
    );
    await createMailbox({
      fromAddress: "mail@example.com",
      provider: "smtp",
      smtp: { host: "smtp.example.com", port: 587, useSsl: true, username: "user", password: "secret" },
    });
    await updateMailbox("box-1", { displayName: null, isActive: false });
    await verifyMailbox("box-1");
    expect(fetch).toHaveBeenNthCalledWith(
      2,
      "/api/v1/communications/mailboxes",
      expect.objectContaining({ method: "POST" }),
    );
    expect(fetch).toHaveBeenNthCalledWith(
      3,
      "/api/v1/communications/mailboxes/box-1",
      expect.objectContaining({ method: "PUT" }),
    );
    expect(fetch).toHaveBeenNthCalledWith(
      4,
      "/api/v1/communications/mailboxes/box-1/verify",
      expect.objectContaining({ method: "POST" }),
    );
    expect(JSON.parse(String(vi.mocked(fetch).mock.calls[1][1]?.body))).toEqual({
      fromAddress: "mail@example.com",
      provider: "smtp",
      smtp: { host: "smtp.example.com", port: 587, useSsl: true, username: "user", password: "secret" },
    });
  });

  it("maps Mailgun create credentials", async () => {
    clearCsrfToken();
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(new Response(JSON.stringify({ token: "csrf-mailgun" }), { status: 200 }))
        .mockResolvedValueOnce(new Response(JSON.stringify({ id: "box-mailgun" }), { status: 201 })),
    );
    await createMailbox({
      fromAddress: "mailgun@example.com",
      displayName: "Mailgun",
      provider: "mailgun",
      mailgun: { domain: "mg.example.com", region: "us", apiKey: "key-secret" },
    });
    expect(JSON.parse(String(vi.mocked(fetch).mock.calls[1][1]?.body))).toEqual({
      fromAddress: "mailgun@example.com",
      displayName: "Mailgun",
      provider: "mailgun",
      mailgun: { domain: "mg.example.com", region: "us", apiKey: "key-secret" },
    });
  });
});
