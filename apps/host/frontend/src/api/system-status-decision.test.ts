import { describe, expect, it } from "vitest";
import { shouldShowMaintenance } from "./system-status";

describe("maintenance gating", () => {
  it("gates normal users during maintenance", () => {
    expect(shouldShowMaintenance({ maintenance: true, message: null }, false)).toBe(true);
  });

  it("does not gate system administrators or failed status loads", () => {
    expect(shouldShowMaintenance({ maintenance: true, message: null }, true)).toBe(false);
    expect(shouldShowMaintenance(undefined, false)).toBe(false);
  });
});
