import { describe, expect, it } from "vitest";
import { contactRoleLabel } from "./contact-role-label";

describe("contactRoleLabel", () => {
  const t = (key: string) => `t:${key}`;

  it("maps each of the three codes to its own catalog key", () => {
    expect(contactRoleLabel(t, "billing")).toBe("t:roleBilling");
    expect(contactRoleLabel(t, "project")).toBe("t:roleProject");
    expect(contactRoleLabel(t, "decision_maker")).toBe("t:roleDecisionMaker");
  });

  it("falls back to the raw code for a role the catalog does not know", () => {
    // The vocabulary is a value change on the server, not a migration, so a
    // widened list reaches an older frontend. Showing the code is worse than
    // showing a name and far better than showing nothing.
    expect(contactRoleLabel(t, "executive_sponsor")).toBe("executive_sponsor");
  });
});
