import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { PaginationMetadata } from "./energy";
import { readJson, request } from "./request";

/**
 * The import of metering points from a file (energy import design D3, D4,
 * D6, D7): an import is a record the page reads, maps, checks, runs and —
 * when rows were left without a start date — attaches; the worker does the
 * work and the page polls while it does.
 */

export type ImportState = "draft" | "checking" | "checked" | "importing" | "done" | "attaching";
export type ImportMode = "create" | "update" | "createOrUpdate";
export type ImportRowOutcome = "created" | "updated" | "failed" | "skipped";
export type ImportRowLink = "none" | "same" | "assigned" | "switched" | "pending" | "attached";

/** One field of the mapping: read from a column of the file, or one value for every row. */
export interface ImportMappingColumn {
  field: string;
  column?: number;
  value?: string;
}

export interface ImportMapping {
  mode: ImportMode;
  columns: ImportMappingColumn[];
}

/** One problem of one row: `column` is the file's 0-based column, null for the row as a whole or a fixed value. */
export interface ImportRowError {
  column: number | null;
  message: string;
}

export interface ImportSampleRow {
  row: number;
  cells: string[];
  errors: ImportRowError[];
}

export interface EnergyImport {
  id: number;
  state: ImportState;
  fileName: string;
  sizeBytes: number;
  encoding: string;
  separator: string;
  rows: number;
  header: string[];
  sample: ImportSampleRow[];
  mapping: ImportMapping | null;
  suggested: ImportMapping;
  processed: number;
  created: number;
  updated: number;
  failed: number;
  skipped: number;
  switched: number;
  pending: number;
  pendingSwitches: number;
  attached: number;
  attachStart: string | null;
  stopRequested: boolean;
  error: string | null;
  createdAt: string;
  createdByName: string | null;
  requestedAt: string | null;
  startedAt: string | null;
  finishedAt: string | null;
}

export interface ImportRow {
  row: number;
  cells: string[];
  outcome: ImportRowOutcome | null;
  link: ImportRowLink | null;
  meteringPointId: number | null;
  gsrn: string | null;
  customerId: number | null;
  customerName: string | null;
  switchFromCustomerId: number | null;
  switchFromCustomerName: string | null;
  supplyStart: string | null;
  errors: ImportRowError[];
}

interface ImportPage<T> {
  data: T[];
  pagination: PaginationMetadata;
}

type RawImportRowError = { column?: number; message: string };
type RawImportSampleRow = Omit<ImportSampleRow, "errors"> & { errors: RawImportRowError[] };
type Optional = "mapping" | "attachStart" | "error" | "createdByName" | "requestedAt" | "startedAt" | "finishedAt";
type RawEnergyImport = Omit<EnergyImport, "sample" | Optional> & {
  sample: RawImportSampleRow[];
} & Partial<{ [K in Optional]: NonNullable<EnergyImport[K]> }>;
type RowOptional = Exclude<keyof ImportRow, "row" | "cells" | "errors">;
type RawImportRow = Pick<ImportRow, "row" | "cells"> & { errors: RawImportRowError[] } & Partial<{
    [K in RowOptional]: NonNullable<ImportRow[K]>;
  }>;

const normalizeError = (raw: RawImportRowError): ImportRowError => ({
  column: raw.column ?? null,
  message: raw.message,
});

export const normalizeImport = (raw: RawEnergyImport): EnergyImport => ({
  ...raw,
  sample: raw.sample.map((row) => ({ ...row, errors: row.errors.map(normalizeError) })),
  mapping: raw.mapping ?? null,
  attachStart: raw.attachStart ?? null,
  error: raw.error ?? null,
  createdByName: raw.createdByName ?? null,
  requestedAt: raw.requestedAt ?? null,
  startedAt: raw.startedAt ?? null,
  finishedAt: raw.finishedAt ?? null,
});

export const normalizeRow = (raw: RawImportRow): ImportRow => ({
  row: raw.row,
  cells: raw.cells,
  outcome: raw.outcome ?? null,
  link: raw.link ?? null,
  meteringPointId: raw.meteringPointId ?? null,
  gsrn: raw.gsrn ?? null,
  customerId: raw.customerId ?? null,
  customerName: raw.customerName ?? null,
  switchFromCustomerId: raw.switchFromCustomerId ?? null,
  switchFromCustomerName: raw.switchFromCustomerName ?? null,
  supplyStart: raw.supplyStart ?? null,
  errors: raw.errors.map(normalizeError),
});

/** The server's own cap: a larger file is refused here rather than sent to be refused. */
export const MAX_IMPORT_FILE_BYTES = 50 * 1024 * 1024;

/** How often the page re-reads an import the worker is working on. */
export const IMPORT_POLL_MS = 2000;

/** Whether the worker is, or should be, working on the import. */
export const isImportRunning = (state: ImportState) =>
  state === "checking" || state === "importing" || state === "attaching";

/** The import's step on the page, derived from its state: attaching belongs to the last step. */
export const importStep = (state: ImportState): 0 | 1 | 2 =>
  state === "draft" ? 0 : state === "checking" || state === "checked" ? 1 : 2;

/** The conflict a run or an attach meets when the number of switches it confirmed is not the server's. */
export const SWITCH_NOT_CONFIRMED = "import_switch_not_confirmed";

export const importsQueryKey = ["energy", "imports"] as const;
export const importQueryKey = (id: number) => ["energy", "imports", id] as const;

/** `POST /energy/imports`: the file as the one part named `file`. Needs `energy:import`. */
export const uploadImport = (file: File): Promise<EnergyImport> => {
  const form = new FormData();
  form.append("file", file, file.name);
  return request<RawEnergyImport>("/api/v1/energy/imports", { method: "POST", body: form }).then(normalizeImport);
};

/** One import, re-read every two seconds while the worker works on it. */
export const importQueryOptions = (id: number) =>
  queryOptions({
    queryKey: importQueryKey(id),
    queryFn: ({ signal }) => request<RawEnergyImport>(`/api/v1/energy/imports/${id}`, { signal }).then(normalizeImport),
    refetchInterval: (query) => (query.state.data && isImportRunning(query.state.data.state) ? IMPORT_POLL_MS : false),
  });

/** The recent imports, newest change first. */
export const importsQueryOptions = (page = 1, pageSize = 10) =>
  queryOptions({
    queryKey: [...importsQueryKey, { page, pageSize }],
    queryFn: async ({ signal }) => {
      const result = await request<ImportPage<RawEnergyImport>>(
        `/api/v1/energy/imports?page=${page}&pageSize=${pageSize}`,
        { signal },
      );
      return { ...result, data: result.data.map(normalizeImport) };
    },
    refetchInterval: (query) =>
      query.state.data?.data.some((item) => isImportRunning(item.state)) ? IMPORT_POLL_MS : false,
  });

/** Which rows to list: one outcome, one link, or both. */
export interface ImportRowsFilter {
  outcome?: ImportRowOutcome;
  link?: ImportRowLink;
}

/** An import's rows of one outcome and/or link, in file order, a page at a time. */
export const importRowsQueryOptions = (id: number, filter: ImportRowsFilter, page = 1, pageSize = 50) =>
  queryOptions({
    queryKey: [...importQueryKey(id), "rows", filter, { page, pageSize }],
    queryFn: async ({ signal }) => {
      const query = new URLSearchParams();
      if (filter.outcome) query.set("outcome", filter.outcome);
      if (filter.link) query.set("link", filter.link);
      query.set("page", String(page));
      query.set("pageSize", String(pageSize));
      const result = await request<ImportPage<RawImportRow>>(`/api/v1/energy/imports/${id}/rows?${query}`, {
        signal,
      });
      return { ...result, data: result.data.map(normalizeRow) };
    },
  });

/** `PUT …/mapping`: saves the mapping and answers the import with its preview. */
export const saveImportMapping = (id: number, mapping: ImportMapping): Promise<EnergyImport> =>
  request<RawEnergyImport>(`/api/v1/energy/imports/${id}/mapping`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(mapping),
  }).then(normalizeImport);

const act = (id: number, action: "check" | "run" | "stop" | "attach", body?: object): Promise<EnergyImport> =>
  request<RawEnergyImport>(`/api/v1/energy/imports/${id}/${action}`, {
    method: "POST",
    ...(body ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : {}),
  }).then(normalizeImport);

export const checkImport = (id: number) => act(id, "check");
export const stopImport = (id: number) => act(id, "stop");

/** `POST …/run`: `confirmSwitches` must be the check's `switched` exactly (design D4). */
export const runImport = (id: number, confirmSwitches: number) => act(id, "run", { confirmSwitches });

/** `POST …/attach`: the pending rows from `start` (a UTC timestamp), `confirmSwitches` the pending switches (design D6). */
export const attachImport = (id: number, input: { start: string; confirmSwitches: number }) => act(id, "attach", input);

export const deleteImport = (id: number): Promise<void> =>
  request<void>(`/api/v1/energy/imports/${id}`, { method: "DELETE" });

/** A file the server handed over, and the name it attached it under. */
export interface FileDownload {
  blob: Blob;
  fileName: string;
}

/**
 * The failed rows as a file to fix and upload again: fetched rather than
 * navigated to, so a refusal is shown as the server words it.
 */
export const downloadFailedRows = async (id: number): Promise<FileDownload> => {
  const fallback = `energy-import-${id}-failed-rows.csv`;
  const response = await fetch(appUrl(`/api/v1/energy/imports/${id}/failed-rows`), { credentials: "include" });
  if (response.ok) {
    const disposition = /filename="?([^";]+)"?/i.exec(response.headers.get("Content-Disposition") ?? "");
    return { blob: await response.blob(), fileName: disposition ? disposition[1] : fallback };
  }
  const problem = await readJson<{ title?: string; detail?: string }>(response).catch(() => null);
  throw Object.assign(new Error(problem?.detail ?? problem?.title ?? `Request failed (HTTP ${response.status})`), {
    status: response.status,
  });
};

/** Hands the file to the browser, then lets go of the object URL, deferred so every browser has started the save. */
export const saveFile = ({ blob, fileName }: FileDownload): void => {
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
 * The fields a mapping may name, in the groups the page shows them in
 * (design D3). The labels are i18n keys of the `energy` catalog. The page
 * hides the customer-link group from a person without
 * `energy:supply-periods-manage`, which the server demands for it.
 */
export interface ImportFieldGroup {
  key: "meteringPoint" | "address" | "customer";
  fields: string[];
}

export const IMPORT_FIELD_GROUPS: readonly ImportFieldGroup[] = [
  {
    key: "meteringPoint",
    fields: ["gsrn", "meterNumber", "priceArea", "gridArea", "connectionStatus", "expectedAnnualConsumptionKwh"],
  },
  { key: "address", fields: ["streetAddress", "postalCode", "city", "countryCode", "latitude", "longitude"] },
  { key: "customer", fields: ["customerLegalId", "supplyStart"] },
];
