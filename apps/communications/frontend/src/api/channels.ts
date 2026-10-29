import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";
export type ChannelProviderKey = "smtp" | "entra";
export type Channel = {
  id: string;
  type: "email";
  address: string;
  displayName: string | null;
  createdAt: string;
  isActive: boolean;
  provider: ChannelProviderKey;
  isDefault: boolean;
  hasCredentials: boolean;
  mailbox?: { id: string } | null;
  settings: {
    host: string | null;
    port: number | null;
    useSsl: boolean | null;
    username: string | null;
  } | null;
  /** A Microsoft 365 channel's receive state; null for SMTP, which only sends. */
  sync?: ChannelSync | null;
};
export type ChannelSync = {
  state: "pending" | "active" | "failed" | "stopped" | (string & {});
  /** The code of the last failure: mailbox_denied, mailbox_gone, mailbox_unknown, sync_failed, provider_unavailable. */
  error: string | null;
  lastSyncedAt: string | null;
};
export type ChannelProvider = {
  key: string;
  displayName: string;
  available: boolean;
  reason: string | null;
  warning: "tenant_wide" | null;
  maxMessageBytes: number | null;
};
export type CreateChannelRequest = {
  type: "email";
  address: string;
  displayName?: string;
  provider: ChannelProviderKey;
  isDefault?: boolean;
  smtp?: { host: string; port: number; useSsl?: boolean; username?: string; password?: string };
};
export type UpdateChannelRequest = {
  displayName?: string | null;
  isActive?: boolean;
  isDefault?: boolean;
  provider?: "smtp";
  smtp?: CreateChannelRequest["smtp"];
};
export const channelsQueryOptions = () =>
  queryOptions({
    queryKey: ["channels"],
    queryFn: ({ signal }) => request<Channel[]>("/api/v1/communications/channels", { signal }),
  });
export const channelProvidersQueryOptions = () =>
  queryOptions({
    queryKey: ["channel-providers"],
    queryFn: ({ signal }) => request<ChannelProvider[]>("/api/v1/communications/channel-providers", { signal }),
  });
export const channelQueryOptions = (id: string) =>
  queryOptions({
    queryKey: ["channel", id],
    queryFn: ({ signal }) => request<Channel>(`/api/v1/communications/channels/${id}`, { signal }),
  });
export const createChannel = (body: CreateChannelRequest) =>
  request<Channel>("/api/v1/communications/channels", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
export const updateChannel = (id: string, body: UpdateChannelRequest) =>
  request<Channel>(`/api/v1/communications/channels/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
export const verifyChannel = (id: string) =>
  request<{ ok: true }>(`/api/v1/communications/channels/${id}/verify`, { method: "POST" });
export const switchChannelProvider = (id: string) =>
  request<Channel>(`/api/v1/communications/channels/${id}/provider`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ provider: "entra" }),
  });
