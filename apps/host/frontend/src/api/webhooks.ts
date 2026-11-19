import type { components } from "../api-schema";
import { integrationQueryKey } from "./integrations";
import { request } from "./request";

/**
 * An integration's webhook endpoints (outgoing webhooks design W1, W25): the
 * HTTPS URLs Vantigo posts the events they list to, each signed with a
 * secret that is in the answer to its creation or roll alone, never in a
 * read.
 */
export type WebhookEndpoint = components["schemas"]["WebhookEndpoint"];
export type WebhookEndpointCreateRequest = components["schemas"]["WebhookEndpointCreateRequest"];
export type WebhookEndpointUpdateRequest = components["schemas"]["WebhookEndpointUpdateRequest"];
export type WebhookEndpointSecret = components["schemas"]["WebhookEndpointSecret"];
export type WebhookEvent = components["schemas"]["WebhookEvent"];

const json = { "Content-Type": "application/json" };
const base = (integrationId: string) => `/api/v1/identity/integrations/${integrationId}/webhooks`;
const post = <T>(path: string, body?: unknown) =>
  request<T>(
    path,
    body === undefined ? { method: "POST" } : { method: "POST", headers: json, body: JSON.stringify(body) },
  );

export const webhooksQueryKey = (integrationId: string) => [...integrationQueryKey(integrationId), "webhooks"] as const;
export const webhookEventsQueryKey = (integrationId: string) =>
  [...integrationQueryKey(integrationId), "webhook-events"] as const;

export const listWebhooks = (integrationId: string) =>
  request<components["schemas"]["WebhookEndpointList"]>(base(integrationId)).then((list) => list.endpoints);
/** The catalogue, each event marked with whether the integration's roles let it receive it now. */
export const listWebhookEvents = (integrationId: string) =>
  request<components["schemas"]["WebhookEventCatalogue"]>(
    `/api/v1/identity/webhook-events?integrationId=${encodeURIComponent(integrationId)}`,
  ).then((catalogue) => catalogue.events);
export const createWebhook = (integrationId: string, body: WebhookEndpointCreateRequest) =>
  post<WebhookEndpointSecret>(base(integrationId), body);
/** Replaces the URL, description (absent clears it) and events under If-Match on the version read. */
export const updateWebhook = (
  integrationId: string,
  webhookId: string,
  version: string,
  body: WebhookEndpointUpdateRequest,
) =>
  request<WebhookEndpoint>(`${base(integrationId)}/${webhookId}`, {
    method: "PATCH",
    headers: { ...json, "If-Match": version },
    body: JSON.stringify(body),
  });
export const deleteWebhook = (integrationId: string, webhookId: string) =>
  request<void>(`${base(integrationId)}/${webhookId}`, { method: "DELETE" });
export const enableWebhook = (integrationId: string, webhookId: string) =>
  post<WebhookEndpoint>(`${base(integrationId)}/${webhookId}/enable`);
export const disableWebhook = (integrationId: string, webhookId: string) =>
  post<WebhookEndpoint>(`${base(integrationId)}/${webhookId}/disable`);
/** Queues a webhook.ping to an enabled endpoint, sent once with no retry (W19). */
export const testWebhook = (integrationId: string, webhookId: string) =>
  post<components["schemas"]["WebhookTestDelivery"]>(`${base(integrationId)}/${webhookId}/test`);
/** A new secret, shown once; the one it replaces signs beside it for overlapHours. */
export const rollWebhookSecret = (integrationId: string, webhookId: string, overlapHours: number) =>
  post<WebhookEndpointSecret>(`${base(integrationId)}/${webhookId}/roll-secret`, { overlapHours });

/**
 * An endpoint's delivery log (W22, W23): every event and test event queued
 * to it in the last 30 days, newest first, by cursor; one delivery with its
 * body and attempts; and resending one that succeeded or is dead.
 */
export type WebhookDelivery = components["schemas"]["WebhookDelivery"];
export type WebhookDeliveryDetail = components["schemas"]["WebhookDeliveryDetail"];
export type WebhookDeliveryPage = components["schemas"]["WebhookDeliveryPage"];
export type WebhookDeliveryState = WebhookDelivery["state"];
export type WebhookDeliveryFilters = { state?: WebhookDeliveryState; event?: string };

export const webhookDeliveriesQueryKey = (integrationId: string, webhookId: string) =>
  [...webhooksQueryKey(integrationId), webhookId, "deliveries"] as const;

export const listWebhookDeliveries = (
  integrationId: string,
  webhookId: string,
  filters: WebhookDeliveryFilters,
  cursor?: string,
) => {
  const query = new URLSearchParams();
  if (filters.state) query.set("state", filters.state);
  if (filters.event) query.set("event", filters.event);
  if (cursor) query.set("cursor", cursor);
  const text = query.toString();
  return request<WebhookDeliveryPage>(`${base(integrationId)}/${webhookId}/deliveries${text ? `?${text}` : ""}`);
};
export const getWebhookDelivery = (integrationId: string, webhookId: string, deliveryId: string) =>
  request<WebhookDeliveryDetail>(`${base(integrationId)}/${webhookId}/deliveries/${deliveryId}`);
/** Sends a delivery again with its webhook-id and body, a fresh run of 8 attempts (W22). */
export const resendWebhookDelivery = (integrationId: string, webhookId: string, deliveryId: string) =>
  post<WebhookDelivery>(`${base(integrationId)}/${webhookId}/deliveries/${deliveryId}/resend`);
