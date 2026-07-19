import { describe, expect, it } from "vitest";
import { resolveSettingsTab } from "./tabs";

describe("resolveSettingsTab", () => {
  it("returns valid tabs as-is", () => {
    expect(resolveSettingsTab("general")).toBe("general");
    expect(resolveSettingsTab("security")).toBe("security");
  });

  it("falls back to general for unknown or missing values", () => {
    expect(resolveSettingsTab(null)).toBe("general");
    expect(resolveSettingsTab("")).toBe("general");
    expect(resolveSettingsTab("bogus")).toBe("general");
  });
});
