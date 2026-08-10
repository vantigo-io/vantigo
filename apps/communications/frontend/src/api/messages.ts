import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { request } from "./request";
export type MessageRecipient = { email: string };
export interface CreateMessageRequest {
  mailboxId?: string;
  subject: string;
  textBody?: string;
  htmlBody?: string;
  to: MessageRecipient[];
  cc?: MessageRecipient[];
  bcc?: MessageRecipient[];
  externalLinks?: ExternalLink[];
  source?: string;
}
export interface CreateMessageResponse {
  messageId: string;
  status: MessageStatus;
  idempotencyKey: string;
}
export type MessageStatus = "queued" | "relay_accepted" | "submission_failed";
export interface MessageListItem {
  id: string;
  subject: string;
  createdAt: string;
  recipientCount: number;
  status: MessageStatus;
  source: string | null;
  archivedAt: string | null;
  mailbox: MailboxSummary | null;
}
export interface MailboxSummary {
  id: string;
  fromAddress: string;
  displayName: string | null;
}
export interface Delivery {
  id: string;
  emailAddress: string;
  recipientType: string;
  status: string;
  attempts: number;
  lastError: string | null;
  acceptedAt: string | null;
}
export interface ExternalLink {
  id: string;
  sourceSystem: string;
  sourceInstance: string;
  entityType: string;
  externalEntityId: string;
  displayLabel: string | null;
}
export interface MessageDetail {
  id: string;
  mailboxId: string;
  subject: string;
  textBody: string | null;
  htmlBody: string | null;
  createdAt: string;
  source: string | null;
  archivedAt: string | null;
  deliveries: Delivery[];
  externalLinks: ExternalLink[];
  mailbox: MailboxSummary | null;
}
export interface MessageEvent {
  id: string;
  deliveryId: string | null;
  eventType: string;
  occurredAt: string;
  dataJson: string | null;
}
export interface Pagination {
  page: number;
  pageSize: number;
  totalCount: number;
  totalPages: number;
  hasNextPage: boolean;
  hasPreviousPage: boolean;
}
export interface Page<T> {
  data: T[];
  pagination: Pagination;
}
export const messagesQueryOptions = (page: number, pageSize = 20, mailboxId?: string, includeArchived = false) =>
  queryOptions({
    queryKey: ["messages", { page, pageSize, mailboxId, includeArchived }],
    queryFn: ({ signal }) =>
      request<Page<MessageListItem>>(
        `/api/v1/communications/messages?page=${page}&pageSize=${pageSize}${mailboxId ? `&mailboxId=${encodeURIComponent(mailboxId)}` : ""}${includeArchived ? "&includeArchived=true" : ""}`,
        { signal },
      ),
    placeholderData: keepPreviousData,
  });
export const messageQueryOptions = (id: string) =>
  queryOptions({
    queryKey: ["message", id],
    queryFn: ({ signal }) =>
      request<MessageDetail>(`/api/v1/communications/messages/${encodeURIComponent(id)}`, { signal }),
  });
export const messageEventsQueryOptions = (id: string, page = 1, pageSize = 50) =>
  queryOptions({
    queryKey: ["message-events", id, { page, pageSize }],
    queryFn: ({ signal }) =>
      request<Page<MessageEvent>>(
        `/api/v1/communications/messages/${encodeURIComponent(id)}/events?page=${page}&pageSize=${pageSize}`,
        { signal },
      ),
  });
export const createMessage = (body: CreateMessageRequest, idempotencyKey: string) =>
  request<CreateMessageResponse>("/api/v1/communications/messages", {
    method: "POST",
    headers: { "Content-Type": "application/json", "Idempotency-Key": idempotencyKey },
    body: JSON.stringify(body),
  });
export type ResendScope = "failed" | "all";
export interface ResendMessageResponse {
  messageId: string;
  status: MessageStatus;
  scope: ResendScope;
  requeuedRecipientCount: number;
}
export const resendMessage = (id: string, scope: ResendScope) =>
  request<ResendMessageResponse>(`/api/v1/communications/messages/${encodeURIComponent(id)}/resend`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ scope }),
  });
export const archiveMessage = (id: string) =>
  request<MessageDetail>(`/api/v1/communications/messages/${encodeURIComponent(id)}/archive`, { method: "POST" });
export const unarchiveMessage = (id: string) =>
  request<MessageDetail>(`/api/v1/communications/messages/${encodeURIComponent(id)}/unarchive`, { method: "POST" });
