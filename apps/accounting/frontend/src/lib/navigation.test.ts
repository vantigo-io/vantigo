import { describe, expect, it } from "vitest";
import { visibleAccountingNavItems } from "./navigation";

describe("the sidebar", () => {
  it("offers a new journal entry only to those who post", () => {
    expect(visibleAccountingNavItems(["accounting:read"]).map((i) => i.key)).not.toContain("journal");
    expect(visibleAccountingNavItems(["accounting:read", "accounting:post"]).map((i) => i.key)).toContain("journal");
    expect(visibleAccountingNavItems([])).toEqual([]);
    expect(visibleAccountingNavItems(["*"]).length).toBe(15);
  });

  it("offers the SAF-T export only to those who export", () => {
    expect(visibleAccountingNavItems(["accounting:read"]).map((i) => i.key)).not.toContain("saft");
    expect(visibleAccountingNavItems(["accounting:read", "accounting:export"]).map((i) => i.key)).toContain("saft");
  });
});
