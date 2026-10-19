import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { CALENDAR_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

export type FeedAddress = Schemas["FeedAddress"];
export type FeedAddresses = Schemas["FeedAddresses"];
export type FeedVariant = Schemas["FeedVariant"];

/** Both variants' addresses, those that exist, and whether they are on the public host (feed design D1). */
export const feedAddressesQueryOptions = () =>
  queryOptions({
    queryKey: [CALENDAR_QUERY_KEY, "feed"],
    queryFn: ({ signal }) => request<FeedAddresses>("/api/v1/calendar/feed", { signal }),
  });

/** Creates the variant's address, or resets it when one exists: the old address is dead from that moment. */
export const resetFeedAddress = (variant: FeedVariant): Promise<FeedAddress> =>
  request<FeedAddress>(`/api/v1/calendar/feed/${variant}`, { method: "POST" });

/** Turns the variant's address off. */
export const deleteFeedAddress = (variant: FeedVariant): Promise<void> =>
  request<void>(`/api/v1/calendar/feed/${variant}`, { method: "DELETE" });
