import { describe, expect, it } from "vitest";
import { projectsCatalog } from "../i18n";
import { projectRoleLabelKey, projectRoles } from "./roles";

describe("projectRoleLabelKey", () => {
  it("names a key the catalog carries in both languages", () => {
    for (const role of projectRoles) {
      const key = projectRoleLabelKey(role);
      expect(projectsCatalog.en).toHaveProperty(key);
      expect(projectsCatalog.nb).toHaveProperty(key);
    }
  });

  it("lists the roles most capable first, the order the people tab renders", () => {
    expect([...projectRoles]).toEqual(["manager", "member", "viewer"]);
  });
});
