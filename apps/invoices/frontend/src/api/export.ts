import { appUrl } from "@vantigo/frontend-shell";
import { ApiValidationError, readJson, sessionExpired } from "./request";

/** A file the server handed over, and the name it attached it under. */
export interface CsvDownload {
  blob: Blob;
  fileName: string;
}

/**
 * The error a download throws once it has signed the person out: the shared
 * client's own sentence and status, so `isSessionExpired` tells a caller there
 * is nothing left to show — the sign-out is the answer.
 */
const signedOut = () => Object.assign(new Error("Your session has expired"), { status: 401 });

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
 * A file, fetched rather than navigated to — the customers module's
 * `downloadFile`, copied: a plain `<a download>` would drop the person on a
 * JSON page when the export is refused, and the export's cap refusal carries
 * no `errors` object, only a problem asking for a narrower period. Reading the
 * body here is what lets it be shown.
 */
export const downloadFile = async (path: string, fallback: string): Promise<CsvDownload> => {
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
    await sessionExpired();
    throw signedOut();
  }
  const problem = await readJson<{ title?: string; detail?: string; errors?: Record<string, string[]> }>(
    response,
  ).catch(() => null);
  if (problem?.errors) throw new ApiValidationError(problem.title ?? "Invalid export", problem.errors, response.status);
  throw Object.assign(new Error(problem?.detail ?? problem?.title ?? `Request failed (HTTP ${response.status})`), {
    status: response.status,
  });
};

/** The accountant's CSV of the issued documents over a range of issue dates (D5). */
export const downloadInvoicesCsv = ({ from, to }: { from: string; to: string }): Promise<CsvDownload> =>
  downloadFile(
    `/api/v1/invoices/export.csv?${new URLSearchParams({ from, to }).toString()}`,
    `invoices-${from}-${to}.csv`,
  );

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
