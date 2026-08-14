import { afterEach, describe, expect, it, vi } from "vitest";
import { loginWithPasskey } from "./account";

describe("passkey client errors", () => {
  afterEach(() => {
    Object.defineProperty(navigator, "credentials", { configurable: true, value: undefined });
    Object.defineProperty(window, "PublicKeyCredential", { configurable: true, value: undefined });
  });

  it("returns a stable unsupported error before starting a ceremony", async () => {
    await expect(loginWithPasskey("person@example.test")).rejects.toMatchObject({
      name: "PasskeyClientError",
      code: "unsupported",
      operation: "sign-in",
    });
  });

  it("returns a stable cancelled error for a dismissed browser ceremony", async () => {
    const credentialConstructor = function PublicKeyCredential() {};
    Object.defineProperty(window, "PublicKeyCredential", { configurable: true, value: credentialConstructor });
    Object.defineProperty(navigator, "credentials", {
      configurable: true,
      value: { get: vi.fn().mockRejectedValue(new DOMException("The request was cancelled", "NotAllowedError")) },
    });
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(new Response(JSON.stringify({ token: "csrf-token" }), { status: 200 }))
        .mockResolvedValueOnce(
          new Response(JSON.stringify({ ceremonyId: "ceremony-id", options: { challenge: "AQ" } }), { status: 200 }),
        ),
    );

    await expect(loginWithPasskey("person@example.test")).rejects.toMatchObject({
      name: "PasskeyClientError",
      code: "cancelled",
      operation: "sign-in",
    });
  });
});
