import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { downloadFile, type FileDownload, refusalOf } from "./documents";

type Schemas = components["schemas"];

/** What an import's rows did, or in a dry run would do (design S19). */
export type StaffImportResult = Schemas["StaffImportResult"];
/** One problem with one row: `row` from 1 under the header, `column` absent for the row as a whole. */
export type StaffImportError = Schemas["StaffImportError"];

/**
 * Sends the register's file to the import: one part named `file`, the
 * `Content-Type` left to the browser so the multipart boundary is its own,
 * and `dryRun` — `true` checks every row and keeps nothing. Fetched here
 * rather than through the shared client, which drops a validation
 * refusal's code: the file refused whole is an `ApiValidationError` on
 * `file` whose `code` (`import_sealed_columns`, `import_not_semicolon`,
 * `import_too_many_rows`, `import_too_large`) the modal words. No answer at
 * all is the browser's `TypeError`.
 */
export const importPeople = async (file: File, dryRun: boolean): Promise<StaffImportResult> => {
  const form = new FormData();
  form.append("file", file, file.name);
  const response = await fetch(appUrl(`/api/v1/staff/people/import?dryRun=${dryRun}`), {
    method: "POST",
    body: form,
    credentials: "include",
  });
  if (response.ok) return (await response.json()) as StaffImportResult;
  throw await refusalOf(response);
};

/**
 * The register as one file — every person, the work and private groups and
 * where each stands today, never a sealed value — named by the server
 * `staff-people-YYYY-MM-DD.csv`; `export_too_many` past the row bound.
 */
export const exportPeople = (): Promise<FileDownload> =>
  downloadFile("/api/v1/staff/people/export.csv", "staff-people.csv");

/** The header row every import the caller may send can carry — the sealed columns only for `staff:personal-data`. */
export const downloadImportTemplate = (): Promise<FileDownload> =>
  downloadFile("/api/v1/staff/people/import-template.csv", "staff-people-import-template.csv");

/**
 * One month's employments for payroll (design S18), `month` as `YYYY-MM`,
 * in the one format there is; the server names it
 * `staff-payroll-YYYY-MM.csv` and records the export with the file's hash.
 */
export const exportPayroll = (month: string): Promise<FileDownload> =>
  downloadFile(
    `/api/v1/staff/payroll/export?${new URLSearchParams({ month, format: "csv" })}`,
    `staff-payroll-${month}.csv`,
  );
