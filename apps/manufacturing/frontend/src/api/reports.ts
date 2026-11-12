import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { handleUnauthorized, MANUFACTURING_QUERY_KEY, queryString, refusalOf, request } from "./request";

type Schemas = components["schemas"];

export type ManufacturingProductionCostReport = Schemas["ManufacturingProductionCostReport"];
export type ManufacturingUtilisationReport = Schemas["ManufacturingUtilisationReport"];
export type ManufacturingTimeDeviationReport = Schemas["ManufacturingTimeDeviationReport"];

/** The three reports (M42), by the path each is read at. */
export type ReportKind = "production-cost" | "utilisation" | "time-deviation";
export const reportKinds: readonly ReportKind[] = ["production-cost", "utilisation", "time-deviation"];

interface ReportAnswers {
  "production-cost": ManufacturingProductionCostReport;
  utilisation: ManufacturingUtilisationReport;
  "time-deviation": ManufacturingTimeDeviationReport;
}

/** A report's filters: each left out takes the server's default (the last 30 days, every site). */
export interface ReportFilter {
  from?: string;
  to?: string;
  siteId?: number;
}

/** A report's query string: only the filters that are set, and `format=csv` for the file. */
export const reportQueryString = (filter: ReportFilter, format: "json" | "csv" = "json"): string =>
  queryString({
    from: filter.from,
    to: filter.to,
    siteId: filter.siteId,
    format: format === "csv" ? "csv" : undefined,
  });

export const reportPath = (kind: ReportKind, filter: ReportFilter, format: "json" | "csv" = "json") =>
  `/api/v1/manufacturing/reports/${kind}${reportQueryString(filter, format)}`;

export const reportQueryOptions = <Kind extends ReportKind>(kind: Kind, filter: ReportFilter) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "reports", kind, filter.from ?? "", filter.to ?? "", filter.siteId ?? ""],
    queryFn: ({ signal }) => request<ReportAnswers[Kind]>(reportPath(kind, filter), { signal }),
  });

/** A file the server handed over, and the name it attached it under. */
export interface CsvDownload {
  blob: Blob;
  fileName: string;
}

/** The name the server attached the file under, or `fallback` when it said nothing. */
const fileNameFrom = (disposition: string | null, fallback: string): string => {
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition ?? "");
  if (encoded) {
    try {
      return decodeURIComponent(encoded[1]);
    } catch {
      // A malformed escape: the plain name (or the fallback) serves instead.
    }
  }
  const plain = /filename="?([^";]+)"?/i.exec(disposition ?? "");
  return plain ? plain[1] : fallback;
};

/**
 * A report as its CSV file, fetched rather than navigated to — the invoices
 * module's `downloadFile`: a plain `<a download>` would drop the person on a
 * problem page when the period is refused; reading the answer here lets the
 * refusal be shown in the page's words.
 */
export const downloadReportCsv = (kind: ReportKind, filter: ReportFilter): Promise<CsvDownload> =>
  downloadCsvFrom(reportPath(kind, filter, "csv"), `manufacturing-${kind}.csv`);

/** Any of the module's CSV files at `path`, read as `downloadReportCsv` reads a report's. */
export const downloadCsvFrom = async (path: string, fallback: string): Promise<CsvDownload> => {
  const response = await fetch(appUrl(path), { credentials: "include" });
  if (response.status === 401) await handleUnauthorized();
  if (!response.ok) throw await refusalOf(response);
  return { blob: await response.blob(), fileName: fileNameFrom(response.headers.get("Content-Disposition"), fallback) };
};

/**
 * Hands the file to the browser, then lets go of the object URL — deferred,
 * because revoking in the same turn as the click is a race some browsers lose.
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
