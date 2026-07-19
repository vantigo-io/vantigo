import { describe, expect, it } from "vitest";
import { isTwoFactorAvailable, requiresTwoFactorSetup, totpSecretFromUri } from "./two-factor";

describe("isTwoFactorAvailable", () => {
  it("is available when enabled", () => {
    expect(isTwoFactorAvailable({ enabled: true, enforced: false })).toBe(true);
  });

  it("is available when only enforced (enforcement implies enabled)", () => {
    expect(isTwoFactorAvailable({ enabled: false, enforced: true })).toBe(true);
  });

  it("is unavailable when both flags are off", () => {
    expect(isTwoFactorAvailable({ enabled: false, enforced: false })).toBe(false);
  });
});

describe("requiresTwoFactorSetup", () => {
  it("requires setup for enforced config and users without 2FA", () => {
    expect(requiresTwoFactorSetup({ enabled: true, enforced: true }, false)).toBe(true);
    expect(requiresTwoFactorSetup({ enabled: false, enforced: true }, null)).toBe(true);
    expect(requiresTwoFactorSetup({ enabled: false, enforced: true }, undefined)).toBe(true);
  });

  it("does not require setup when 2FA is already enabled", () => {
    expect(requiresTwoFactorSetup({ enabled: true, enforced: true }, true)).toBe(false);
  });

  it("does not require setup when not enforced", () => {
    expect(requiresTwoFactorSetup({ enabled: true, enforced: false }, false)).toBe(false);
    expect(requiresTwoFactorSetup({ enabled: false, enforced: false }, false)).toBe(false);
  });
});

describe("totpSecretFromUri", () => {
  it("extracts the secret from an otpauth URI", () => {
    expect(
      totpSecretFromUri(
        "otpauth://totp/Vantigo.Customers:user%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Vantigo.Customers",
      ),
    ).toBe("JBSWY3DPEHPK3PXP");
  });

  it("returns null for invalid URIs", () => {
    expect(totpSecretFromUri("not a uri")).toBe(null);
    expect(totpSecretFromUri("otpauth://totp/foo")).toBe(null);
  });
});
