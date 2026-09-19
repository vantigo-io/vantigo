import { describe, expect, it } from "vitest";
import { adminCatalog, hostPermissionTranslationKeys } from "./admin";

// This is not a link to the server: the projects module's permission catalog
// is declared in apps/server/internal/projects/module.go (`var permissions`),
// which the host cannot read from a unit test, so this list is hand-kept in
// step with it rather than derived. A seventh permission there passes this
// test silently until somebody extends the list below — extend it here
// rather than adding a second test when that happens. (A live parity check
// would need to call `GET /authorization/permissions` at runtime, which is a
// different, and slower, kind of test than this one.)
const projectsPermissionKeys = [
  "projects:access",
  "projects:create",
  "projects:view-all",
  "projects:manage-all",
  "projects:view-financials",
  "projects:view-costs",
] as const;

describe("the admin permission catalog", () => {
  it("has a display name and a description, in English and Norwegian, for these six projects permissions", () => {
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
