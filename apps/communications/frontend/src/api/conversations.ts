import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export type ConversationStatus = "open" | "closed" | "archived";
export type Participant = {
  id: string;
  channelId: string;
  address: string;
  displayName: string | null;
  contactId: number | null;
};
export type Tag = { id: string; name: string; color: string | null };
export type Attachment = {
  id: string;
  fileName: string;
  contentType: string;
  sizeBytes: number;
  contentId: string | null;
  scanStatus: string;
  isInline: boolean;
  createdAt: string;
  downloadAvailable: boolean;
  downloadPath: string;
};
export type AttachmentUpload = {
  id: string;
  fileName: string;
  contentType: string;
  sizeBytes: number;
  scanStatus: string;
  isInline: boolean;
  expiresAt: string;
  ready: boolean;
};
export type ReplyRecipients = {
  canReply: boolean;
  canReplyAll: boolean;
  replyTo: string | null;
  replyAllCc: Participant[];
};
export type Delivery = {
  id: string;
  destination: string;
  recipientType: string;
  status: string;
  attempts: number;
  error: string | null;
  acceptedAt: string | null;
};
export type ConversationMessage = {
  id: string;
  direction: "inbound" | "outbound" | "internal_note";
  participant: Participant | null;
  authorUserId: string | null;
  subject: string | null;
  textBody: string | null;
  htmlBody: string | null;
  occurredAt: string;
  createdAt: string;
  attachments: Attachment[];
  deliveries: Delivery[];
};
export type ConversationListItem = {
  id: string;
  channelId: string;
  subject: string | null;
  status: ConversationStatus;
  assignedUserId: string | null;
  customerId: number | null;
  customerAssociationSource: string | null;
  suggestedCustomerId: number | null;
  candidateCustomerIds: number[];
  lastActivityAt: string;
  previewText: string | null;
  participants: Participant[];
  unread: boolean;
  tags: Tag[];
};
export type ConversationDetail = ConversationListItem & {
  suggestedCustomerConfidence: number | null;
  suggestedCustomerReasoning: string | null;
  createdAt: string;
  messages: ConversationMessage[];
  lastReadAt: string | null;
  replyRecipients: ReplyRecipients;
};
export type AiDraftResponse = {
  interactionId: string;
  subject: string | null;
  text: string;
  productDataUsed: boolean;
  notice: string;
};
export type AiCustomerSuggestionResponse = {
  interactionId: string;
  customerId: number | null;
  confidence: number | null;
  rationale: string | null;
  outcome: string;
};
export type Page<T> = {
  data: T[];
  pagination: {
    page: number;
    pageSize: number;
    totalCount: number;
    totalPages: number;
    hasNextPage: boolean;
    hasPreviousPage: boolean;
  };
};
export type ConversationFilters = {
  page?: number;
  pageSize?: number;
  status?: ConversationStatus;
  assignedUserId?: string;
  tagId?: string;
  customerId?: number;
  unreadOnly?: boolean;
};

const queryString = (filters: ConversationFilters) => {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filters))
    if (value !== undefined && value !== "") params.set(key, String(value));
  const query = params.toString();
  return query ? `?${query}` : "";
};
export const conversationsQueryOptions = (filters: ConversationFilters = {}) =>
  queryOptions({
    queryKey: ["conversations", filters],
    queryFn: ({ signal }) =>
      request<Page<ConversationListItem>>(`/api/v1/communications/conversations${queryString(filters)}`, { signal }),
    placeholderData: keepPreviousData,
  });
export const conversationQueryOptions = (id: string) =>
  queryOptions({
    queryKey: ["conversation", id],
    queryFn: ({ signal }) =>
      request<ConversationDetail>(`/api/v1/communications/conversations/${encodeURIComponent(id)}`, { signal }),
    enabled: Boolean(id),
  });
export const markConversationRead = (id: string) =>
  request<void>(`/api/v1/communications/conversations/${encodeURIComponent(id)}/read`, { method: "POST" });
export const replyToConversation = (
  id: string,
  body: {
    textBody?: string;
    htmlBody?: string;
    subject?: string;
    replyMode: "reply" | "reply_all";
    attachmentIds?: string[];
  },
  idempotencyKey: string,
) =>
  request<{ conversationId: string; messageId: string | null; status: string; idempotencyKey: string | null }>(
    `/api/v1/communications/conversations/${encodeURIComponent(id)}/reply`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json", "Idempotency-Key": idempotencyKey },
      body: JSON.stringify(body),
    },
  );
export const stageConversationAttachment = (conversationId: string, file: File, idempotencyKey: string) => {
  const body = new FormData();
  body.append("file", file);
  return request<AttachmentUpload>(
    `/api/v1/communications/conversations/${encodeURIComponent(conversationId)}/attachments`,
    { method: "POST", headers: { "Idempotency-Key": idempotencyKey }, body },
  );
};
export const attachmentDownloadUrl = (path: string) => path;
export const attachmentUploadStatusQueryOptions = (conversationId: string, attachmentId: string) =>
  queryOptions({
    queryKey: ["conversation-attachment-upload", conversationId, attachmentId],
    queryFn: ({ signal }) =>
      request<AttachmentUpload>(
        `/api/v1/communications/conversations/${encodeURIComponent(conversationId)}/attachments/${encodeURIComponent(attachmentId)}`,
        { signal },
      ),
    enabled: Boolean(conversationId && attachmentId),
    refetchInterval: (query) => (["pending", "scanning"].includes(query.state.data?.scanStatus ?? "") ? 2000 : false),
  });
export const addConversationNote = (id: string, textBody: string) =>
  request<{ conversationId: string; messageId: string | null; status: string }>(
    `/api/v1/communications/conversations/${encodeURIComponent(id)}/notes`,
    { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ textBody }) },
  );
export const updateConversation = (
  id: string,
  body: { status?: ConversationStatus; assignedUserId?: string | null; customerId?: number | null },
) =>
  request<
    Pick<
      ConversationDetail,
      | "id"
      | "status"
      | "assignedUserId"
      | "customerId"
      | "customerAssociationSource"
      | "suggestedCustomerId"
      | "candidateCustomerIds"
    >
  >(`/api/v1/communications/conversations/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
export const addTag = (conversationId: string, tagId: string) =>
  request<void>(`/api/v1/communications/conversations/${conversationId}/tags/${tagId}`, { method: "PUT" });
export const removeTag = (conversationId: string, tagId: string) =>
  request<void>(`/api/v1/communications/conversations/${conversationId}/tags/${tagId}`, { method: "DELETE" });
export const tagsQueryOptions = () =>
  queryOptions({
    queryKey: ["communication-tags"],
    queryFn: ({ signal }) => request<Tag[]>("/api/v1/communications/tags", { signal }),
  });
export const createTag = (body: { name: string; color?: string }) =>
  request<Tag>("/api/v1/communications/tags", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
export const draftConversationWithAi = (
  id: string,
  body: { tone: "concise" | "friendly" | "formal"; instruction: string },
) =>
  request<AiDraftResponse>(`/api/v1/communications/conversations/${encodeURIComponent(id)}/ai/draft`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
export const suggestConversationCustomerWithAi = (id: string) =>
  request<AiCustomerSuggestionResponse>(
    `/api/v1/communications/conversations/${encodeURIComponent(id)}/ai/customer-suggestion`,
    { method: "POST" },
  );

/** Only use when the API has supplied sanitized HTML. The sandbox prevents scripts, forms, popups and network access. */
export const safeHtmlSrcDoc = (html: string) =>
  `<!doctype html><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'"><style>body{font:14px system-ui;color:#253248;margin:0;line-height:1.55}img{max-width:100%}</style>${html}`;
