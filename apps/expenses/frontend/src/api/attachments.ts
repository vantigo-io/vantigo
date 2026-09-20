import { appUrl } from "@vantigo/frontend-shell";
import type { ExpenseAttachment } from "./entries";
import { request } from "./request";

export type { ExpenseAttachment } from "./entries";

/**
 * Where a receipt's bytes are read from. There is no signed URL and never
 * will be: the download is served over the session cookie, with the expense's
 * own visibility, so anyone who may not see the expense gets the same bare
 * 404 an unknown id gets. An `<img src>` carries the cookie on a same-origin
 * request, which is why images can be shown inline without fetching them.
 */
export const attachmentUrl = (id: number): string => appUrl(`/api/v1/expenses/attachments/${id}`);

/**
 * Attaches one receipt to an outlay. The body is `multipart/form-data` with
 * exactly one part named `file`, and the `Content-Type` is deliberately not
 * set by hand — the boundary has to come from the browser.
 *
 * Answers 201 with the attachment; 400 naming `file` (what was uploaded) or
 * `entryId` (what it was uploaded onto); 403 when the expense is not the
 * caller's to change; 404 when it is not theirs to see; 429 when the office
 * has run through the upload allowance it shares; 503 when the store is down.
 */
export const uploadReceipt = (entryId: number, file: File): Promise<ExpenseAttachment> => {
  const form = new FormData();
  form.append("file", file, file.name);
  return request<ExpenseAttachment>(`/api/v1/expenses/entries/${entryId}/attachments`, { method: "POST", body: form });
};

/** Removes a receipt while its expense is still its owner's to change. */
export const deleteReceipt = (id: number): Promise<void> =>
  request<void>(`/api/v1/expenses/attachments/${id}`, { method: "DELETE" });
