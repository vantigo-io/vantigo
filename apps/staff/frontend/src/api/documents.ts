import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import {
  ApiConflictError,
  ApiValidationError,
  json,
  NotFoundError,
  readJson,
  request,
  STAFF_QUERY_KEY,
} from "./request";

type Schemas = components["schemas"];

export type StaffDocument = Schemas["StaffDocument"];
export type StaffDocumentCategory = Schemas["StaffDocumentCategory"];
export type StaffDocumentCategorySummary = Schemas["StaffDocumentCategorySummary"];
export type StaffDocumentCategoryInput = Schemas["StaffDocumentCategoryRequest"];
export type StaffDocumentCategoryChange = Schemas["StaffDocumentCategoryChangeRequest"];
export type StaffDocumentChange = Schemas["StaffDocumentChangeRequest"];
export type StaffDocumentClass = Schemas["StaffDocumentClass"];

/**
 * A person's archive, newest first, each with its category. Who reads it is
 * the server's (design S14): the person, `staff:manage` without the health
 * documents, `staff:personal-data` all; anyone else gets the unknown id's 404.
 */
export const documentsQueryOptions = (personId: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", personId, "documents"],
    queryFn: ({ signal }) => request<StaffDocument[]>(`/api/v1/staff/people/${personId}/documents`, { signal }),
  });

/** The caller's own documents, health documents included; `no_person` (404) when they are linked to nobody. */
export const myDocumentsQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "me", "documents"],
    queryFn: ({ signal }) => request<StaffDocument[]>("/api/v1/staff/me/documents", { signal }),
  });

/** Every category in order, archived ones included — `staff:view`'s, `staff:manage`'s and `staff:personal-data`'s. */
export const documentCategoriesQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "document-categories"],
    queryFn: ({ signal }) => request<StaffDocumentCategory[]>("/api/v1/staff/document-categories", { signal }),
  });

export interface DocumentUpload {
  file: File;
  categoryId: number;
  validTo?: string;
  signedAt?: string;
}

/**
 * Files one document: `multipart/form-data` with the bytes in the part
 * `file` and the category and dates as fields, the `Content-Type` left to
 * the browser so the boundary is its own. Refused 400 on `file`
 * (`document_type_unsupported`, `document_too_large`) or on a field, 403
 * `category_restricted` for a health category without `staff:personal-data`,
 * 409 `document_count_exceeded` at 200, 503 `storage_unavailable`.
 */
export const uploadDocument = (
  personId: number,
  { file, categoryId, validTo, signedAt }: DocumentUpload,
): Promise<StaffDocument> => {
  const form = new FormData();
  form.append("file", file, file.name);
  form.append("categoryId", String(categoryId));
  if (validTo) form.append("validTo", validTo);
  if (signedAt) form.append("signedAt", signedAt);
  return request<StaffDocument>(`/api/v1/staff/people/${personId}/documents`, { method: "POST", body: form });
};

/** Replaces the category and the dates under `If-Match`; a date left out is cleared, the file never changes. */
export const updateDocument = (id: number, input: StaffDocumentChange, revision: number): Promise<StaffDocument> =>
  request<StaffDocument>(`/api/v1/staff/documents/${id}`, json("PUT", input, revision));

/** Removes the document and its file for good. The contract takes no `If-Match` here. */
export const deleteDocument = (id: number): Promise<void> =>
  request<void>(`/api/v1/staff/documents/${id}`, { method: "DELETE" });

export const createDocumentCategory = (input: StaffDocumentCategoryInput): Promise<StaffDocumentCategory> =>
  request<StaffDocumentCategory>("/api/v1/staff/document-categories", json("POST", input));

/** A company category's names and whether it is archived; refused `seed_immutable` and `category_in_use` (409). */
export const updateDocumentCategory = (
  id: number,
  input: StaffDocumentCategoryChange,
  revision: number,
): Promise<StaffDocumentCategory> =>
  request<StaffDocumentCategory>(`/api/v1/staff/document-categories/${id}`, json("PUT", input, revision));

/** A file the server handed over, and the name it attached it under. */
export interface FileDownload {
  blob: Blob;
  fileName: string;
}

/** The name in `Content-Disposition` — the RFC 5987 `filename*` first — or `fallback` when it names none. */
export const fileNameFrom = (disposition: string | null, fallback: string): string => {
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition ?? "");
  if (encoded) {
    // A malformed escape is a URIError; the plain name or the fallback serves instead.
    try {
      return decodeURIComponent(encoded[1]);
    } catch {
      // fall through
    }
  }
  // Anchored to a parameter of its own and its `=`, so a `filename*` in
  // another charset is never read as the plain name.
  const plain = /(?:^|;)\s*filename\s*=\s*"?([^";]+)"?/i.exec(disposition ?? "");
  return plain ? plain[1] : fallback;
};

/**
 * A file fetched with the session rather than navigated to — the customers
 * download: a bare link would land the person on a problem page when the
 * read is refused, and the bytes are only theirs to read over the session
 * cookie. A refusal is thrown as the shared client throws it, so the page
 * says it in the same words.
 */
const downloadFile = async (path: string, fallback: string): Promise<FileDownload> => {
  const response = await fetch(appUrl(path), { credentials: "include" });
  if (response.ok)
    return {
      blob: await response.blob(),
      fileName: fileNameFrom(response.headers.get("Content-Disposition"), fallback),
    };
  const problem = await readJson<{ title?: string; detail?: string; code?: string; errors?: Record<string, string[]> }>(
    response,
  ).catch(() => null);
  const message = problem?.detail ?? problem?.title ?? `Request failed (HTTP ${response.status})`;
  if (response.status === 404) throw new NotFoundError(message);
  if (problem?.errors) throw new ApiValidationError(message, problem.errors, response.status);
  if (response.status === 409 && problem?.title)
    throw new ApiConflictError(message, { title: problem.title, detail: problem.detail, code: problem.code, problem });
  throw Object.assign(new Error(message), { status: response.status, code: problem?.code });
};

/** One document's bytes, named as the server names them. */
export const downloadDocument = (document: StaffDocument): Promise<FileDownload> =>
  downloadFile(`/api/v1/staff/documents/${document.id}`, document.fileName);

/** The caller's own copy of their data (`GET /me/export`), named as the server names it. */
export const downloadMyData = (): Promise<FileDownload> =>
  downloadFile("/api/v1/staff/me/export", "personal-data.json");

/**
 * Hands the file to the browser, then lets go of the object URL — deferred,
 * because revoking in the same turn as the click is a race Chromium wins and
 * Firefox and Safari lose (the customers `saveCsv`).
 */
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
