import { queryOptions } from "@tanstack/react-query";
import { type CsvDownload, downloadFile } from "./import-export";
import { request } from "./request";

/**
 * The timeline entry an attachment belongs to, as the card names it beside
 * the file (customer attachments design D3): a deleted entry keeps its
 * attachments, and `state` says so.
 */
export interface CustomerAttachmentEntry {
  id: number;
  eventType: string;
  occurredOn: string;
  summary: string;
  state: string;
}

/** One of a customer's attachments (design D1–D3). The bytes are read through `downloadAttachment`. */
export interface CustomerAttachment {
  id: number;
  customerId: number;
  fileName: string;
  contentType: string;
  sizeBytes: number;
  uploadedAt: string;
  /** Null for a document of the relationship itself; the wire omits the key. */
  timelineEntry: CustomerAttachmentEntry | null;
}

type RawCustomerAttachment = Omit<CustomerAttachment, "timelineEntry"> & {
  timelineEntry?: CustomerAttachmentEntry | null;
};

export const normalizeAttachment = (raw: RawCustomerAttachment): CustomerAttachment => ({
  ...raw,
  timelineEntry: raw.timelineEntry ?? null,
});

/** The key every attachment write invalidates: the card and the timeline read the same list. */
export const attachmentsQueryKey = (customerId: number) => ["customers", customerId, "attachments"] as const;

/** `GET /customers/{id}/attachments`: newest first, each with its entry. Needs `customers:timeline-view`. */
export const customerAttachmentsQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: attachmentsQueryKey(customerId),
    queryFn: async ({ signal }) => {
      const page = await request<{ data: RawCustomerAttachment[] }>(`/api/v1/customers/${customerId}/attachments`, {
        signal,
      });
      return page.data.map(normalizeAttachment);
    },
  });

/**
 * Sends one file: a part named `file`, the `Content-Type` left to the browser
 * so the multipart boundary is its own, and `timelineEntryId` when the file
 * belongs to a manual entry. Refused 400 on `file` (the type, the size) or on
 * `timelineEntryId`, 409 `attachment_count_exceeded` at 200, 409
 * `customer_merged` / `customer_anonymised` for a read-only customer, 503
 * when the store cannot be written.
 */
export const uploadAttachment = (
  customerId: number,
  file: File,
  timelineEntryId?: number | null,
): Promise<CustomerAttachment> => {
  const form = new FormData();
  form.append("file", file, file.name);
  if (timelineEntryId != null) form.append("timelineEntryId", String(timelineEntryId));
  return request<RawCustomerAttachment>(`/api/v1/customers/${customerId}/attachments`, {
    method: "POST",
    body: form,
  }).then(normalizeAttachment);
};

/** Removes the attachment and, once the removal has committed, its file. */
export const deleteAttachment = (customerId: number, attachmentId: number): Promise<void> =>
  request<void>(`/api/v1/customers/${customerId}/attachments/${attachmentId}`, { method: "DELETE" });

/**
 * One attachment's bytes, fetched with the session rather than navigated to
 * — the personal-data export's shape: a bare link would land the person on a
 * problem page when the read is refused, and the bytes are only theirs to
 * read over the session cookie. Named as the server names it.
 */
export const downloadAttachment = (attachment: CustomerAttachment): Promise<CsvDownload> =>
  downloadFile(`/api/v1/customers/${attachment.customerId}/attachments/${attachment.id}`, attachment.fileName);
