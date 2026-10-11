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

// Hand-kept in step with `apps/server/internal/invoices/module.go` (`var
// permissions`), for the same reason.
const invoicesPermissionKeys = [
  "invoices:access",
  "invoices:create",
  "invoices:issue",
  "invoices:manage",
  "invoices:payments",
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

  it("has a display name and a description, in English and Norwegian, for the five invoices permissions", () => {
    for (const key of invoicesPermissionKeys) {
      const translation = hostPermissionTranslationKeys[key as keyof typeof hostPermissionTranslationKeys];
      expect(translation, `no catalog entry for ${key}`).toBeDefined();
      for (const lng of ["en", "nb"] as const) {
        const catalog = adminCatalog[lng] as Record<string, string>;
        expect(catalog[translation.displayNameKey], `${key} display name (${lng})`).toBeTruthy();
        expect(catalog[translation.descriptionKey], `${key} description (${lng})`).toBeTruthy();
      }
    }
  });

  it("matches the server's exact English display names and descriptions for invoices permissions", () => {
    const en = adminCatalog.en as Record<string, string>;
    expect(en["admin.permission.invoicesAccess"]).toBe("Use Invoices");
    expect(en["admin.permission.invoicesAccessDescription"]).toBe(
      "Use the Invoices app and read every invoice, credit note, PDF, payment and delivery, the journal, the CSV export and the stats; the overdue list and an invoice's reminder letters and their PDFs, hold, hand-off, manual deliveries and charges; the collection rates, the reminder settings and a customer's reminder policy; and the attention items about overdue invoices and refunds due.",
    );
    expect(en["admin.permission.invoicesCreate"]).toBe("Create invoices");
    expect(en["admin.permission.invoicesCreateDescription"]).toBe(
      "Create, edit and delete invoice drafts, and preview a draft as PDF.",
    );
    expect(en["admin.permission.invoicesIssue"]).toBe("Issue invoices");
    expect(en["admin.permission.invoicesIssueDescription"]).toBe(
      "Issue a draft into a numbered document that can never be changed, create credit notes, send an issued document by e-mail or as EHF, cancel or resolve its EHF transmissions, and record that an invoice was handed over or posted, or remove such a record.",
    );
    expect(en["admin.permission.invoicesManage"]).toBe("Manage invoicing");
    expect(en["admin.permission.invoicesManageDescription"]).toBe(
      "Change the seller record and its Peppol id, the number series start, the KID agreement, the e-invoicing access point's credentials, the VAT codes and their rates, the reminder settings and the regime review, the collection rates — add one ahead of a release, or delete one nothing has relied on — and the format a bank account's files are imported in.",
    );
    expect(en["admin.permission.invoicesPayments"]).toBe("Register payments");
    expect(en["admin.permission.invoicesPaymentsDescription"]).toBe(
      "Register payments against issued invoices and remove a registration with a reason; import bank files, read the imported files, the bank accounts and their lines, and work the exception queue; make and read reminder runs, print paper letters and confirm them posted or reprint them, and withdraw and retry letters; hold a disputed invoice, hand one to collection and export the collection file; register and remove charge payments and waive charges; set a customer's reminder policy; and see the attention items about the bank lines, the letters and the print batches.",
    );
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

  // Hand-kept in step with apps/server/internal/customers/module.go, like the
  // lists above: customers:merge and customers:personal-data are the keys the
  // last two deliveries added.
  it("names customers:merge in English and Norwegian, the English the server's own", () => {
    const translation = hostPermissionTranslationKeys["customers:merge"];
    expect(translation, "no catalog entry for customers:merge").toBeDefined();
    for (const lng of ["en", "nb"] as const) {
      const catalog = adminCatalog[lng] as Record<string, string>;
      expect(catalog[translation.displayNameKey], `display name (${lng})`).toBeTruthy();
      expect(catalog[translation.descriptionKey], `description (${lng})`).toBeTruthy();
    }
    const en = adminCatalog.en as Record<string, string>;
    expect(en[translation.displayNameKey]).toBe("Merge customers");
    expect(en[translation.descriptionKey]).toBe(
      "Merge a duplicate customer into another, moving its contacts, addresses, timeline, tags and other modules' references, and archiving it.",
    );
  });

  it("names customers:personal-data in English and Norwegian, the English the server's own", () => {
    const translation = hostPermissionTranslationKeys["customers:personal-data"];
    expect(translation, "no catalog entry for customers:personal-data").toBeDefined();
    for (const lng of ["en", "nb"] as const) {
      const catalog = adminCatalog[lng] as Record<string, string>;
      expect(catalog[translation.displayNameKey], `display name (${lng})`).toBeTruthy();
      expect(catalog[translation.descriptionKey], `description (${lng})`).toBeTruthy();
    }
    const en = adminCatalog.en as Record<string, string>;
    expect(en[translation.displayNameKey]).toBe("Manage personal data");
    expect(en[translation.descriptionKey]).toBe(
      "Hand a private person all the data held about them, and schedule the anonymisation of an archived private person.",
    );
  });
});
