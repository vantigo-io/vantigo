import { afterEach, describe, expect, it, vi } from "vitest";
import { getProfile, loginWithPasskey, updateProfile } from "./account";

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

describe("the profile's colour scheme", () => {
  afterEach(() => vi.unstubAllGlobals());

  const account = (colorScheme: unknown) => ({
    id: "1",
    displayName: "Kari",
    email: "kari@example.test",
    preferredLanguage: "nb",
    avatarUrl: null,
    colorScheme,
  });
  const answer = (body: unknown) =>
    vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
      );

  it("reads light and dark as they are, and null, a missing value or anything else as auto", async () => {
    for (const [sent, want] of [
      ["light", "light"],
      ["dark", "dark"],
      [null, "auto"],
      [undefined, "auto"],
      ["sepia", "auto"],
    ] as const) {
      vi.stubGlobal("fetch", answer(account(sent)));
      expect((await getProfile()).colorScheme).toBe(want);
    }
  });

  it("saves auto as automatic, light and dark as they are, and leaves it out when it is not named", async () => {
    const bodies: Record<string, unknown>[] = [];
    for (const colorScheme of ["auto", "light", "dark", undefined] as const) {
      const fetchMock = answer(account("dark"));
      vi.stubGlobal("fetch", fetchMock);
      await updateProfile({ displayName: "Kari", preferredLanguage: "nb", colorScheme });
      bodies.push(JSON.parse(fetchMock.mock.calls[0][1].body));
    }
    expect(bodies.map((b) => b.colorScheme)).toEqual(["automatic", "light", "dark", undefined]);
    expect(bodies[3]).not.toHaveProperty("colorScheme");
    expect(bodies[3]).not.toHaveProperty("timeZone");
    expect(bodies[0]).toMatchObject({ displayName: "Kari", preferredLanguage: "nb" });
  });
});
