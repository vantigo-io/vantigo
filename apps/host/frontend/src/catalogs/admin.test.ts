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

// Hand-kept in step with `apps/server/internal/expenses/module.go` (`var
// permissions`) for the same reason the projects list above is: the host
// cannot read the server's permission catalog from a unit test.
const expensesPermissionKeys = ["expenses:access", "expenses:approve", "expenses:view-all", "expenses:manage"] as const;

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

  it("has a display name and a description, in English and Norwegian, for these four expenses permissions", () => {
    for (const key of expensesPermissionKeys) {
      const translation = hostPermissionTranslationKeys[key as keyof typeof hostPermissionTranslationKeys];
      expect(translation, `no catalog entry for ${key}`).toBeDefined();
      for (const lng of ["en", "nb"] as const) {
        const catalog = adminCatalog[lng] as Record<string, string>;
        expect(catalog[translation.displayNameKey], `${key} display name (${lng})`).toBeTruthy();
        expect(catalog[translation.descriptionKey], `${key} description (${lng})`).toBeTruthy();
      }
    }
  });

  // The English text is pinned verbatim against the server's own strings
  // (`apps/server/internal/expenses/module.go`), the way a translation catalog
  // should track its source of truth rather than paraphrase it.
  it("matches the server's exact English display names and descriptions for expenses permissions", () => {
    const en = adminCatalog.en as Record<string, string>;
    expect(en["admin.permission.expensesAccess"]).toBe("Use Expenses");
    expect(en["admin.permission.expensesAccessDescription"]).toBe(
      "Use the Expenses app and record and submit your own expenses.",
    );
    expect(en["admin.permission.expensesApprove"]).toBe("Approve expenses");
    expect(en["admin.permission.expensesApproveDescription"]).toBe(
      "Approve or reject anyone's expenses, including those with no project.",
    );
    expect(en["admin.permission.expensesViewAll"]).toBe("View all expenses");
    expect(en["admin.permission.expensesViewAllDescription"]).toBe("See everyone's expenses.");
    expect(en["admin.permission.expensesManage"]).toBe("Manage expenses");
    expect(en["admin.permission.expensesManageDescription"]).toBe(
      "Change expense settings, rates and categories, record expenses for a colleague, mark expenses reimbursed, and work past the period lock.",
    );
  });
});
