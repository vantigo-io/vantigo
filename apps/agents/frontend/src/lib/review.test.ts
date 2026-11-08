import { describe, expect, it } from "vitest";
import { agentsCatalog } from "../i18n";
import { discardReasons, heldCodes, heldKey, isUrgent, msUntil, remaining } from "./review";

describe("the review queue's words", () => {
  it("words every code a held item may carry, and an unknown one as other", () => {
    for (const code of heldCodes) expect(agentsCatalog.en, code).toHaveProperty([heldKey(code)]);
    expect(heldKey("S99")).toBe("held.other");
    expect(agentsCatalog.en).toHaveProperty(["held.other"]);
  });

  it("words every discard reason", () => {
    for (const reason of discardReasons) expect(agentsCatalog.en, reason).toHaveProperty([`discard.${reason}`]);
  });
});

describe("the countdown", () => {
  const now = Date.parse("2026-11-08T10:00:00Z");

  it("counts to an instant and stops at zero", () => {
    expect(msUntil("2026-11-08T10:30:00Z", now)).toBe(30 * 60_000);
    expect(msUntil("2026-11-08T09:00:00Z", now)).toBe(0);
    expect(msUntil(null, now)).toBeNull();
    expect(msUntil("not a date", now)).toBeNull();
  });

  it("names the two largest units", () => {
    expect(remaining(26 * 3_600_000 + 5 * 60_000)).toEqual({ kind: "daysHours", days: 1, hours: 2 });
    expect(remaining(3 * 3_600_000 + 20 * 60_000)).toEqual({ kind: "hoursMinutes", hours: 3, minutes: 20 });
    expect(remaining(12 * 60_000 + 30_000)).toEqual({ kind: "minutes", minutes: 12 });
    expect(remaining(30_000)).toEqual({ kind: "lessThanMinute" });
    expect(remaining(0)).toEqual({ kind: "lessThanMinute" });
  });

  it("is urgent within the hour, or whenever a Messenger window is counting", () => {
    expect(isUrgent("2026-11-08T10:40:00Z", null, now)).toBe(true);
    expect(isUrgent("2026-11-08T18:00:00Z", null, now)).toBe(false);
    expect(isUrgent("2026-11-08T18:00:00Z", "2026-11-08T18:15:00Z", now)).toBe(true);
  });
});
