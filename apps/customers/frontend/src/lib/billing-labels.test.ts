import { describe, expect, it } from "vitest";
import { billingWarningMessage, EHF_AVAILABLE_CODE, KNOWN_BILLING_WARNING_CODES } from "./billing-labels";

const t = (key: string) => key;

describe("ehf_recipient_not_registered (design D4)", () => {
  it("is a known warning code, so the card's yellow list renders it", () => {
    expect(KNOWN_BILLING_WARNING_CODES).toContain("ehf_recipient_not_registered");
  });

  it("has a message key", () => {
    expect(billingWarningMessage(t, "ehf_recipient_not_registered")).toBe("warningEhfRecipientNotRegistered");
  });
});

describe("ehf_available (design D4)", () => {
  it("is NOT a known warning code — it is an offer, not a problem, so the yellow list must never show it", () => {
    expect(KNOWN_BILLING_WARNING_CODES).not.toContain(EHF_AVAILABLE_CODE);
  });

  it("has no warning message (nothing to render in the yellow list even if a caller forgets to filter it out)", () => {
    expect(billingWarningMessage(t, "ehf_available")).toBeNull();
  });

  it("names the code the offer alert keys off", () => {
    expect(EHF_AVAILABLE_CODE).toBe("ehf_available");
  });
});
