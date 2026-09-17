import { describe, expect, it } from "vitest";
import { mfaEnrolmentDestination, mfaEnrolmentRedirect } from "./mfa-enrolment-gate";

const gated = { mfaEnrollmentRequired: true };
const clear = { mfaEnrollmentRequired: false };

describe("mfaEnrolmentRedirect", () => {
  it("sends a session that must enrol to the security settings from anywhere in the app", () => {
    for (const path of ["/", "/dashboard", "/admin", "/admin/status", "/customers", "/workspace/users"]) {
      expect(mfaEnrolmentRedirect(gated, path)).toBe(mfaEnrolmentDestination);
    }
  });

  it("lets a session that must enrol stay anywhere under /settings", () => {
    for (const path of ["/settings", "/settings/security", "/settings/profile"]) {
      expect(mfaEnrolmentRedirect(gated, path)).toBeUndefined();
    }
  });

  it("does not treat a path that merely starts with the word settings as the settings tree", () => {
    expect(mfaEnrolmentRedirect(gated, "/settingsx")).toBe(mfaEnrolmentDestination);
  });

  it("leaves public paths alone, so sign-out and sign-in still work", () => {
    for (const path of ["/sign-in", "/session-expired", "/setup"]) {
      expect(mfaEnrolmentRedirect(gated, path)).toBeUndefined();
    }
  });

  it("does nothing for a session that has enrolled, or is not held to MFA", () => {
    expect(mfaEnrolmentRedirect(clear, "/dashboard")).toBeUndefined();
    expect(mfaEnrolmentRedirect({}, "/dashboard")).toBeUndefined();
  });

  it("does nothing without a session: that is the sign-in redirect's job", () => {
    expect(mfaEnrolmentRedirect(null, "/dashboard")).toBeUndefined();
    expect(mfaEnrolmentRedirect(undefined, "/dashboard")).toBeUndefined();
  });
});
