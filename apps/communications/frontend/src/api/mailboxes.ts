import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export interface Mailbox {
  id: string;
  fromAddress: string;
  displayName: string | null;
  createdAt: string;
  isActive: boolean;
  provider: MailboxProvider;
  isDefault: boolean;
  hasCredentials: boolean;
  settings: MailboxSettings | null;
}

export type MailboxProvider = "smtp" | "mailgun";

export interface SmtpMailboxSettings {
  host: string;
  port: number;
  useSsl: boolean;
  username?: string;
  password?: string;
}

export interface MailgunMailboxSettings {
  domain: string;
  region: "us" | "eu";
  apiKey: string;
}

export interface MailboxSettings {
  host?: string;
  port?: number;
  useSsl?: boolean;
  username?: string;
  domain?: string;
  region?: "us" | "eu";
}

export type CreateMailboxRequest = {
  fromAddress: string;
  displayName?: string;
  isDefault?: boolean;
} & (
  | { provider: "smtp"; smtp: SmtpMailboxSettings; mailgun?: never }
  | { provider: "mailgun"; mailgun: MailgunMailboxSettings; smtp?: never }
);

export interface UpdateMailboxRequest {
  displayName?: string | null;
  isActive?: boolean;
  isDefault?: boolean;
  provider?: MailboxProvider;
  smtp?: SmtpMailboxSettings;
  mailgun?: MailgunMailboxSettings;
}

export const mailboxesQueryOptions = () =>
  queryOptions({
    queryKey: ["mailboxes"],
    queryFn: ({ signal }) => request<Mailbox[]>("/api/v1/communications/mailboxes", { signal }),
  });

export const mailboxQueryOptions = (id: string) =>
  queryOptions({
    queryKey: ["mailbox", id],
    queryFn: ({ signal }) => request<Mailbox>(`/api/v1/communications/mailboxes/${encodeURIComponent(id)}`, { signal }),
  });

export const createMailbox = (body: CreateMailboxRequest) =>
  request<Mailbox>("/api/v1/communications/mailboxes", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

export const updateMailbox = (id: string, body: UpdateMailboxRequest) =>
  request<Mailbox>(`/api/v1/communications/mailboxes/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

export const verifyMailbox = (id: string) =>
  request<{ ok: true }>(`/api/v1/communications/mailboxes/${encodeURIComponent(id)}/verify`, {
    method: "POST",
  });
