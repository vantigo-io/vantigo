import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { CALENDAR_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

export type CalendarSubscription = Schemas["CalendarSubscription"];
export type CalendarSubscriptionWrite = Schemas["CalendarSubscriptionWrite"];
export type CalendarSubscriptionColour = Schemas["CalendarSubscriptionColour"];

/** The refusal code of the eleventh subscription (feed design D6). */
export const SUBSCRIPTION_LIMIT = "subscription_limit";

const subscriptionPath = (id: number) => `/api/v1/calendar/subscriptions/${id}`;

/** The caller's own subscriptions, by name, each with the worker's record of its last fetch. */
export const subscriptionsQueryOptions = () =>
  queryOptions({
    queryKey: [CALENDAR_QUERY_KEY, "subscriptions"],
    queryFn: ({ signal }) => request<CalendarSubscription[]>("/api/v1/calendar/subscriptions", { signal }),
  });

/** Subscribes, fetching once before the answer: a failed fetch is the row's `error`, not a refusal. */
export const createSubscription = (input: CalendarSubscriptionWrite): Promise<CalendarSubscription> =>
  request<CalendarSubscription>("/api/v1/calendar/subscriptions", json("POST", input));

/** A whole replace; a new address is fetched once before the answer. */
export const updateSubscription = (id: number, input: CalendarSubscriptionWrite): Promise<CalendarSubscription> =>
  request<CalendarSubscription>(subscriptionPath(id), json("PUT", input));

export const deleteSubscription = (id: number): Promise<void> =>
  request<void>(subscriptionPath(id), { method: "DELETE" });

/** Fetches it now, answering the row as the fetch left it. */
export const refreshSubscription = (id: number): Promise<CalendarSubscription> =>
  request<CalendarSubscription>(`${subscriptionPath(id)}/refresh`, { method: "POST" });
