import { queryOptions } from "@tanstack/react-query";
import type { PaginatedResponse } from "./customers";
import { type CsvDownload, downloadFile } from "./import-export";
import { request } from "./request";

/**
 * The import of customers from a file (customers import wizard design D1,
 * D3, D5, D7): an import is a record the page reads, maps, checks and runs;
 * the worker does the work and the page polls while it does.
 */

export type ImportState = "draft" | "checking" | "checked" | "importing" | "done";
export type ImportMode = "create" | "update" | "createOrUpdate";
export type ImportMatchBy = "customerNumber" | "legalId";
export type ImportRowOutcome = "created" | "updated" | "failed";

/** One field of the mapping: read from a column of the file, or one value for every row. */
export interface ImportMappingColumn {
  field: string;
  column?: number;
  value?: string;
}

export interface ImportMapping {
  mode: ImportMode;
  matchBy?: ImportMatchBy;
  allowDuplicateIdentity: boolean;
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

export interface CustomerImport {
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
  customerId: number | null;
  customerNumber: number | null;
  errors: ImportRowError[];
}

type RawImportRowError = { column?: number; message: string };
type RawImportSampleRow = Omit<ImportSampleRow, "errors"> & { errors: RawImportRowError[] };
type RawCustomerImport = Omit<
  CustomerImport,
  "sample" | "mapping" | "error" | "createdByName" | "requestedAt" | "startedAt" | "finishedAt"
> & {
  sample: RawImportSampleRow[];
  mapping?: ImportMapping;
  error?: string;
  createdByName?: string;
  requestedAt?: string;
  startedAt?: string;
  finishedAt?: string;
};
type RawImportRow = Omit<ImportRow, "outcome" | "customerId" | "customerNumber" | "errors"> & {
  outcome?: ImportRowOutcome;
  customerId?: number;
  customerNumber?: number;
  errors: RawImportRowError[];
};

const normalizeError = (raw: RawImportRowError): ImportRowError => ({
  column: raw.column ?? null,
  message: raw.message,
});

export const normalizeImport = (raw: RawCustomerImport): CustomerImport => ({
  ...raw,
  sample: raw.sample.map((row) => ({ ...row, errors: row.errors.map(normalizeError) })),
  mapping: raw.mapping ?? null,
  error: raw.error ?? null,
  createdByName: raw.createdByName ?? null,
  requestedAt: raw.requestedAt ?? null,
  startedAt: raw.startedAt ?? null,
  finishedAt: raw.finishedAt ?? null,
});

const normalizeRow = (raw: RawImportRow): ImportRow => ({
  ...raw,
  outcome: raw.outcome ?? null,
  customerId: raw.customerId ?? null,
  customerNumber: raw.customerNumber ?? null,
  errors: raw.errors.map(normalizeError),
});

/** The server's own cap (`maxImportUploadBytes`): a larger file is refused here rather than sent to be refused. */
export const MAX_IMPORT_FILE_BYTES = 50 * 1024 * 1024;

/** How often the page re-reads an import the worker is working on. */
export const IMPORT_POLL_MS = 2000;

/** Whether the worker is, or should be, working on the import. */
export const isImportRunning = (state: ImportState) => state === "checking" || state === "importing";

/** The import's step on the page, derived from its state (design D8). */
export const importStep = (state: ImportState): 0 | 1 | 2 =>
  state === "draft" ? 0 : state === "checking" || state === "checked" ? 1 : 2;

export const importsQueryKey = ["customers", "imports"] as const;
export const importQueryKey = (id: number) => ["customers", "imports", id] as const;

/** `POST /customers/imports`: the file as the one part named `file`. Needs `customers:import`. */
export const uploadImport = (file: File): Promise<CustomerImport> => {
  const form = new FormData();
  form.append("file", file, file.name);
  return request<RawCustomerImport>("/api/v1/customers/imports", { method: "POST", body: form }).then(normalizeImport);
};

/** One import, re-read every two seconds while the worker works on it. */
export const importQueryOptions = (id: number) =>
  queryOptions({
    queryKey: importQueryKey(id),
    queryFn: ({ signal }) =>
      request<RawCustomerImport>(`/api/v1/customers/imports/${id}`, { signal }).then(normalizeImport),
    refetchInterval: (query) => (query.state.data && isImportRunning(query.state.data.state) ? IMPORT_POLL_MS : false),
  });

/** The recent imports, newest change first. */
export const importsQueryOptions = (page = 1, pageSize = 10) =>
  queryOptions({
    queryKey: [...importsQueryKey, { page, pageSize }],
    queryFn: async ({ signal }) => {
      const result = await request<PaginatedResponse<RawCustomerImport>>(
        `/api/v1/customers/imports?page=${page}&pageSize=${pageSize}`,
        { signal },
      );
      return { ...result, data: result.data.map(normalizeImport) };
    },
    refetchInterval: (query) =>
      query.state.data?.data.some((item) => isImportRunning(item.state)) ? IMPORT_POLL_MS : false,
  });

/** An import's rows of one outcome, in file order, a page at a time. */
export const importRowsQueryOptions = (id: number, outcome: ImportRowOutcome, page = 1, pageSize = 50) =>
  queryOptions({
    queryKey: [...importQueryKey(id), "rows", outcome, { page, pageSize }],
    queryFn: async ({ signal }) => {
      const result = await request<PaginatedResponse<RawImportRow>>(
        `/api/v1/customers/imports/${id}/rows?outcome=${outcome}&page=${page}&pageSize=${pageSize}`,
        { signal },
      );
      return { ...result, data: result.data.map(normalizeRow) };
    },
  });

/** `PUT …/mapping`: saves the mapping and answers the import with its preview. */
export const saveImportMapping = (id: number, mapping: ImportMapping): Promise<CustomerImport> =>
  request<RawCustomerImport>(`/api/v1/customers/imports/${id}/mapping`, {
    method: "PUT",
    body: JSON.stringify(mapping),
  }).then(normalizeImport);

const act = (id: number, action: "check" | "run" | "stop"): Promise<CustomerImport> =>
  request<RawCustomerImport>(`/api/v1/customers/imports/${id}/${action}`, { method: "POST" }).then(normalizeImport);

export const checkImport = (id: number) => act(id, "check");
export const runImport = (id: number) => act(id, "run");
export const stopImport = (id: number) => act(id, "stop");

export const deleteImport = (id: number): Promise<void> =>
  request<void>(`/api/v1/customers/imports/${id}`, { method: "DELETE" });

/** The failed rows as a file to fix and upload again. */
export const downloadFailedRows = (id: number): Promise<CsvDownload> =>
  downloadFile(`/api/v1/customers/imports/${id}/failed-rows`, `customers-import-${id}-failed-rows.csv`);

/**
 * The fields a mapping may name, in the groups the reference names them by
 * (design D3). The labels are i18n keys of the `customers` catalog. A group
 * carries the permission its fields need, for the page to hide what the
 * person may not map.
 */
export interface ImportFieldGroup {
  key: "customer" | "identity" | "contact" | "postal" | "invoice" | "billing" | "relationship";
  fields: string[];
}

export const IMPORT_FIELD_GROUPS: readonly ImportFieldGroup[] = [
  { key: "customer", fields: ["customerNumber", "name", "type", "status"] },
  { key: "identity", fields: ["legalCountry", "legalType", "legalId", "legalName"] },
  { key: "contact", fields: ["email", "phone", "website"] },
  {
    key: "postal",
    fields: ["postalLine1", "postalLine2", "postalPostalCode", "postalCity", "postalRegion", "postalCountry"],
  },
  {
    key: "invoice",
    fields: ["invoiceLine1", "invoiceLine2", "invoicePostalCode", "invoiceCity", "invoiceRegion", "invoiceCountry"],
  },
  {
    key: "billing",
    fields: [
      "invoiceEmail",
      "reminderEmail",
      "paymentTermsDays",
      "currency",
      "language",
      "invoiceDelivery",
      "reminderDelivery",
      "peppolId",
      "gln",
      "buyerReference",
      "defaultBillRate",
    ],
  },
  { key: "relationship", fields: ["group", "tags"] },
];

/** The group a field belongs to. */
export const importFieldGroup = (field: string): ImportFieldGroup["key"] | undefined =>
  IMPORT_FIELD_GROUPS.find((group) => group.fields.includes(field))?.key;
