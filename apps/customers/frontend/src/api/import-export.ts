import { appUrl } from "@vantigo/frontend-shell";
import { type CustomersQueryParams, customersSearchParams } from "./customers";
import { ApiValidationError, handleUnauthorized, readJson, request } from "./request";

/** A file the server handed over, and the name it attached it under. */
export interface CsvDownload {
  blob: Blob;
  fileName: string;
}

/**
 * One problem with one row of an import (customers import/export design D3):
 * `row` is the 1-based data row, `column` the header of a field's error — null
 * for the row as a whole, which the wire says by omitting it.
 */
export interface CustomerImportError {
  row: number;
  column: string | null;
  message: string;
}

/** What an import did, or in a dry run would do. */
export interface CustomerImportResult {
  dryRun: boolean;
  rows: number;
  created: number;
  updated: number;
  failed: number;
  errors: CustomerImportError[];
}

type RawCustomerImportResult = Omit<CustomerImportResult, "errors"> & {
  errors: Array<Omit<CustomerImportError, "column"> & { column?: string }>;
};

/**
 * The error a download throws once it has signed the person out: the shared
 * client's own sentence and status, so `isSessionExpired` tells a caller there
 * is nothing left to show — the sign-out is the answer.
 */
const sessionExpired = () => Object.assign(new Error("Your session has expired"), { status: 401 });

/** Whether `error` is an expired session this package has already handed to the host. */
export const isSessionExpired = (error: unknown): boolean => (error as { status?: unknown } | null)?.status === 401;

/** The name the server attached the file under, or `fallback` when it said nothing. */
const fileNameFrom = (disposition: string | null, fallback: string): string => {
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition ?? "");
  if (encoded) {
    // A malformed escape is a URIError; the download itself is fine, so the
    // plain name (or the fallback) serves instead.
    try {
      return decodeURIComponent(encoded[1]);
    } catch {
      // fall through
    }
  }
  const plain = /filename="?([^";]+)"?/i.exec(disposition ?? "");
  return plain ? plain[1] : fallback;
};

/**
 * A file, fetched rather than navigated to — the reimbursements download: a
 * plain `<a download>` would drop the person on a JSON page when the export is
 * refused, and the export's cap refusal carries no `errors` object, only a
 * detail asking for a narrower filter. Reading the body here is what lets it be
 * shown.
 */
const downloadCsv = async (path: string, fallback: string): Promise<CsvDownload> => {
  const response = await fetch(appUrl(path), { credentials: "include" });
  if (response.ok) {
    return {
      blob: await response.blob(),
      fileName: fileNameFrom(response.headers.get("Content-Disposition"), fallback),
    };
  }
  // The shared client is what signs somebody out; this request does not go
  // through it, so an expired session is handed over by hand rather than shown
  // as a raw problem sentence.
  if (response.status === 401) {
    await handleUnauthorized();
    throw sessionExpired();
  }
  const problem = await readJson<{ title?: string; detail?: string; errors?: Record<string, string[]> }>(
    response,
  ).catch(() => null);
  if (problem?.errors) throw new ApiValidationError(problem.title ?? "Invalid export", problem.errors, response.status);
  throw Object.assign(new Error(problem?.detail ?? problem?.title ?? `Request failed (HTTP ${response.status})`), {
    status: response.status,
  });
};

/** The list, as the caller sees it, as the customers file: its filters and sort, never its paging. */
export const downloadCustomersCsv = (params: CustomersQueryParams): Promise<CsvDownload> =>
  downloadCsv(
    `/api/v1/customers/export${customersSearchParams({ ...params, page: undefined, pageSize: undefined })}`,
    "customers.csv",
  );

/** The header every import may carry. */
export const downloadImportTemplate = (): Promise<CsvDownload> =>
  downloadCsv("/api/v1/customers/import/template", "customers-import-template.csv");

/**
 * Hands the file to the browser, then lets go of the object URL — deferred,
 * because revoking in the same turn as the click is a race Chromium wins and
 * Firefox and Safari lose (the reimbursements `saveCsv`).
 */
export const saveCsv = ({ blob, fileName }: CsvDownload): void => {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = fileName;
  document.body.append(link);
  link.click();
  setTimeout(() => {
    link.remove();
    URL.revokeObjectURL(url);
  }, 0);
};

/**
 * Sends the file to the import: one part named `file`, the `Content-Type` left
 * to the browser so the multipart boundary is its own. `dryRun: true` keeps
 * nothing. A file the server refuses as a whole is an `ApiValidationError`
 * whose `fields.file` says why.
 */
export const importCustomers = async (
  file: File,
  options: { dryRun: boolean; allowDuplicateIdentity: boolean },
): Promise<CustomerImportResult> => {
  const form = new FormData();
  form.append("file", file, file.name);
  const query = new URLSearchParams({
    dryRun: String(options.dryRun),
    allowDuplicateIdentity: String(options.allowDuplicateIdentity),
  });
  const raw = await request<RawCustomerImportResult>(`/api/v1/customers/import?${query}`, {
    method: "POST",
    body: form,
  });
  return { ...raw, errors: raw.errors.map((error) => ({ ...error, column: error.column ?? null })) };
};
