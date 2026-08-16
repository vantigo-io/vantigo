import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";
export type Channel = {
  id: string;
  type: "email";
  address: string;
  displayName: string | null;
  createdAt: string;
  isActive: boolean;
  provider: "smtp" | "mailgun";
  isDefault: boolean;
  hasCredentials: boolean;
  settings: {
    host: string | null;
    port: number | null;
    useSsl: boolean | null;
    username: string | null;
    domain: string | null;
    region: string | null;
  } | null;
};
export type CreateChannelRequest = {
  type: "email";
  address: string;
  displayName?: string;
  provider: "smtp" | "mailgun";
  isDefault?: boolean;
  smtp?: { host: string; port: number; useSsl?: boolean; username?: string; password?: string };
  mailgun?: { domain: string; region: "us" | "eu"; apiKey: string; inboundSigningKey: string };
};
export type UpdateChannelRequest = {
  displayName?: string | null;
  isActive?: boolean;
  isDefault?: boolean;
  provider?: "smtp" | "mailgun";
  smtp?: CreateChannelRequest["smtp"];
  mailgun?: CreateChannelRequest["mailgun"];
};
export const channelsQueryOptions = () =>
  queryOptions({
    queryKey: ["channels"],
    queryFn: ({ signal }) => request<Channel[]>("/api/v1/communications/channels", { signal }),
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
