import { describe, expect, it, vi } from "vitest";
import { creationOptions, credentialJson, requestOptions } from "./webauthn";

describe("WebAuthn serialization", () => {
  const useFallback = () => {
    const credentialConstructor = function PublicKeyCredential() {};
    vi.stubGlobal("PublicKeyCredential", credentialConstructor);
    Object.defineProperty(window, "PublicKeyCredential", { configurable: true, value: credentialConstructor });
    Object.defineProperty(credentialConstructor, "parseRequestOptionsFromJSON", { value: undefined });
    Object.defineProperty(credentialConstructor, "parseCreationOptionsFromJSON", { value: undefined });
  };

  it("manually serializes credentials without invoking toJSON", () => {
    const credential = {
      id: "credential-id",
      rawId: Uint8Array.from([0, 255]).buffer,
      type: "public-key",
      response: {
        clientDataJSON: Uint8Array.from([1]).buffer,
        authenticatorData: Uint8Array.from([2]).buffer,
        signature: Uint8Array.from([3]).buffer,
        userHandle: null,
      },
      getClientExtensionResults: () => ({ nested: { bytes: Uint8Array.from([4]) } }),
      toJSON: vi.fn(() => {
        throw new TypeError("Illegal invocation");
      }),
    } as unknown as Credential;

    expect(credentialJson(credential)).toEqual({
      id: "credential-id",
      rawId: "AP8",
      type: "public-key",
      response: { clientDataJSON: "AQ", authenticatorData: "Ag", signature: "Aw", userHandle: null },
      clientExtensionResults: { nested: { bytes: "BA" } },
      authenticatorAttachment: undefined,
    });
    expect((credential as Credential & { toJSON: ReturnType<typeof vi.fn> }).toJSON).not.toHaveBeenCalled();
  });

  it("decodes only binary request option fields in the fallback", () => {
    useFallback();
    const input = {
      challenge: "AQ",
      rp: { name: "AQ" },
      user: { id: "Ag", name: "user" },
      allowCredentials: [{ id: "Aw", type: "public-key" }],
    };
    const result = requestOptions(input);
    expect(result.challenge).toEqual(Uint8Array.from([1]));
    expect(result.allowCredentials?.[0].id).toEqual(Uint8Array.from([3]));
    expect((result as unknown as { rp: { name: string } }).rp.name).toBe("AQ");
  });

  it("keeps creation fallback strings unchanged except binary fields", () => {
    useFallback();
    const result = creationOptions({
      challenge: "AQ",
      rp: { name: "AQ", id: "example.test" },
      user: { id: "Ag", name: "user", displayName: "User" },
      pubKeyCredParams: [],
    });
    expect(result.challenge).toEqual(Uint8Array.from([1]));
    expect(result.rp).toEqual({ name: "AQ", id: "example.test" });
    expect(result.user.id).toEqual(Uint8Array.from([2]));
  });
});
