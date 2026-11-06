import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, META_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/**
 * The workspace's Meta settings (Messenger design N10): the app, the webhook's
 * address and verify token, the Human Agent switch, the Pages and the status
 * line. The app secret and the Page tokens are never answered.
 */
export type MetaSettings = Schemas["MetaSettings"];
export type MetaSettingsRequest = Schemas["MetaSettingsRequest"];
export type MetaPage = Schemas["MetaPage"];
export type MetaStatus = Schemas["MetaStatus"];

const base = "/api/v1/meta";

export const settingsQueryOptions = () =>
  queryOptions({
    queryKey: [META_QUERY_KEY, "settings"],
    queryFn: ({ signal }) => request<MetaSettings>(`${base}/settings`, { signal }),
  });

/** Sets what is sent; a field left out keeps its value, an empty secret keeps the stored one. */
export const updateSettings = (body: MetaSettingsRequest) =>
  request<MetaSettings>(`${base}/settings`, json("PUT", body));

/** Replaces the webhook's verify token with a new random one. */
export const regenerateVerifyToken = () => request<MetaSettings>(`${base}/settings/verify-token`, { method: "POST" });

/** Connects a Page by its access token, which Vantigo checks with Meta and seals at once. */
export const addPage = (accessToken: string) => request<MetaPage>(`${base}/pages`, json("POST", { accessToken }));

export const removePage = (pageId: string) =>
  request<void>(`${base}/pages/${encodeURIComponent(pageId)}`, { method: "DELETE" });
