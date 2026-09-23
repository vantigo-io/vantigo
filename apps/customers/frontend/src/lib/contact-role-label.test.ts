import { describe, expect, it } from "vitest";
import { contactRoleLabel, primaryContactLabel } from "./contact-role-label";

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

describe("primaryContactLabel", () => {
  const t = (key: string, options?: Record<string, unknown>) =>
    options ? `t:${key}:${JSON.stringify(options)}` : `t:${key}`;

  it("reads a whole composed catalog key for each of the three known roles", () => {
    // Not an interpolated template: nb's compounds close up
    // ("fakturakontakt"), which a template interpolating a noun cannot produce.
    expect(primaryContactLabel(t, "billing")).toBe("t:primaryRoleForBilling");
    expect(primaryContactLabel(t, "project")).toBe("t:primaryRoleForProject");
    expect(primaryContactLabel(t, "decision_maker")).toBe("t:primaryRoleForDecisionMaker");
  });

  it("falls back to the interpolated template for a role the catalog does not know", () => {
    expect(primaryContactLabel(t, "executive_sponsor")).toBe('t:primaryRoleFor:{"role":"executive_sponsor"}');
  });
});
