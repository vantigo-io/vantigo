import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { type ListFilters, listQuery } from "./list-query";
import { json, ORDERS_QUERY_KEY, request, send } from "./request";
import type {
  Change,
  Subscription,
  SubscriptionInput,
  SubscriptionPage,
  SubscriptionStatus,
  SubscriptionUpdate,
} from "./types";

export interface SubscriptionListFilters extends ListFilters {
  status?: SubscriptionStatus[];
}

const base = "/api/v1/orders/subscriptions";

/** One page of subscriptions, newest first (O42); the shown page stays while the next is fetched. */
export const subscriptionListQueryOptions = (filters: SubscriptionListFilters) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "subscriptions", "list", filters],
    queryFn: ({ signal }) => request<SubscriptionPage>(`${base}${listQuery(filters)}`, { signal }),
    placeholderData: keepPreviousData,
  });

export const subscriptionQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "subscriptions", "detail", id],
    queryFn: ({ signal }) => request<Subscription>(`${base}/${id}`, { signal }),
  });

export const subscriptionChangesQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "subscriptions", "changes", id],
    queryFn: ({ signal }) => request<{ items: Change[] }>(`${base}/${id}/changes`, { signal }),
  });

export const createSubscription = (input: SubscriptionInput) => send<Subscription>(base, json("POST", input));
export const updateSubscription = (id: number, input: SubscriptionUpdate) =>
  send<Subscription>(`${base}/${id}`, json("PUT", input));

/** What every write sends of the subscription as the page read it (O44): its version and its `updatedAt`. */
export interface SubscriptionRead {
  version: number;
  updatedAt: string;
}
const token = (read: SubscriptionRead) => ({ version: read.version, expectedUpdatedAt: read.updatedAt });

/** The steps of a subscription's life (O42, O44); pause, resume and end take a date. */
export type SubscriptionStep = "activate" | "cancel" | "pause" | "resume" | "end";

/** `dates` carries a pause's or a resume's `from`, or an end's `endDate`. */
export const stepSubscription = (
  id: number,
  step: SubscriptionStep,
  read: SubscriptionRead,
  dates: { from?: string; endDate?: string } = {},
) => send<Subscription>(`${base}/${id}/${step}`, json("POST", { ...token(read), ...dates }));
