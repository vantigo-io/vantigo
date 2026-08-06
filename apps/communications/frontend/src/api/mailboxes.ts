import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export interface Mailbox {
  id: string;
  fromAddress: string;
  displayName: string | null;
  createdAt: string;
  isActive: boolean;
}

export interface CreateMailboxRequest {
  fromAddress: string;
  displayName?: string;
}

export interface UpdateMailboxRequest {
  displayName?: string | null;
  isActive?: boolean;
}

export const mailboxesQueryOptions = () =>
  queryOptions({
    queryKey: ["mailboxes"],
    queryFn: ({ signal }) => request<Mailbox[]>("/api/v1/mailboxes", { signal }),
  });

export const mailboxQueryOptions = (id: string) =>
  queryOptions({
    queryKey: ["mailbox", id],
    queryFn: ({ signal }) => request<Mailbox>(`/api/v1/mailboxes/${encodeURIComponent(id)}`, { signal }),
  });

export const createMailbox = (body: CreateMailboxRequest) =>
  request<Mailbox>("/api/v1/mailboxes", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

export const updateMailbox = (id: string, body: UpdateMailboxRequest) =>
  request<Mailbox>(`/api/v1/mailboxes/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
