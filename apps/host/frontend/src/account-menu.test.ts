import { describe, expect, it } from "vitest";
import { accountMenuSections } from "./account-menu";
import { visibleNavSections } from "./navigation";

const context = (overrides: Partial<Parameters<typeof visibleNavSections>[1]> = {}) => ({
  permissions: ["*"],
  isOwner: false,
  canManageAuthorization: false,
  enabledModules: [],
  ...overrides,
});
const visible = (overrides: Parameters<typeof context>[0] = {}) =>
  visibleNavSections(accountMenuSections, context(overrides)).map((section) => ({
    label: section.label,
    items: section.items.map((item) => item.to),
  }));

describe("account menu catalog", () => {
  it("shows a plain member only their own account section", () => {
    expect(visible()).toEqual([{ label: "navigation.accountSection", items: ["/settings"] }]);
  });

  it("adds the workspace section for owners and authorization managers", () => {
    expect(visible({ isOwner: true })).toEqual([
      { label: "navigation.accountSection", items: ["/settings"] },
      { label: "navigation.workspaceSection", items: ["/workspace/overview"] },
    ]);
    expect(visible({ canManageAuthorization: true })[1]).toEqual({
      label: "navigation.workspaceSection",
      items: ["/workspace/roles"],
    });
    expect(visible({ isOwner: true, canManageAuthorization: true })[1]?.items).toEqual([
      "/workspace/overview",
      "/workspace/roles",
    ]);
  });

  it("adds the system section only for system admins", () => {
    expect(visible({ isOwner: true, canManageAuthorization: true }).map((section) => section.label)).not.toContain(
      "navigation.systemSection",
    );
    expect(visible({ isSystemAdmin: true }).at(-1)).toEqual({ label: "navigation.systemSection", items: ["/admin"] });
  });

  it("keeps workspace administration on its own segment and off the users/invitations tabs", () => {
    const items = accountMenuSections.flatMap((section) => section.items);
    for (const item of items.filter((item) => item.ownerOnly || item.capability)) {
      expect(item.to.startsWith("/workspace/")).toBe(true);
    }
    expect(items.map((item) => item.to)).not.toContain("/workspace/users");
    expect(items.map((item) => item.to)).not.toContain("/workspace/invitations");
  });
});
