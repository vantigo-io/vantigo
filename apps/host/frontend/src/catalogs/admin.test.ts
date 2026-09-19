import { describe, expect, it } from "vitest";
import { adminCatalog, hostPermissionTranslationKeys } from "./admin";

// The Go module declares the projects permissions the host must be able to
// name; nothing else pins that the admin catalog kept up as they grew from
// five to six with projects:view-costs, so this is that guard. Extend the
// list here rather than adding a second test if a future permission arrives.
const projectsPermissionKeys = [
  "projects:access",
  "projects:create",
  "projects:view-all",
  "projects:manage-all",
  "projects:view-financials",
  "projects:view-costs",
] as const;

describe("the admin permission catalog", () => {
  it("has a display name and a description, in English and Norwegian, for every projects permission", () => {
    for (const key of projectsPermissionKeys) {
      const translation = hostPermissionTranslationKeys[key as keyof typeof hostPermissionTranslationKeys];
      expect(translation, `no catalog entry for ${key}`).toBeDefined();
      for (const lng of ["en", "nb"] as const) {
        const catalog = adminCatalog[lng] as Record<string, string>;
        expect(catalog[translation.displayNameKey], `${key} display name (${lng})`).toBeTruthy();
        expect(catalog[translation.descriptionKey], `${key} description (${lng})`).toBeTruthy();
      }
    }
  });
});
