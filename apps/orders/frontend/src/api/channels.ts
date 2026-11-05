import { queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, request } from "./request";

/** A Communications mail channel, as its list answers one (`communications:channels-manage`). */
export interface ChannelOption {
  id: string;
  address: string;
  displayName?: string | null;
  isActive: boolean;
  available?: boolean;
  provider: string;
}

/** The active mail channels the settings' send channel may name (O25). */
export const channelsQueryOptions = () =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "channels"],
    retry: false,
    queryFn: ({ signal }) => request<ChannelOption[]>("/api/v1/communications/channels", { signal }),
    select: (channels) => channels.filter((channel) => channel.isActive),
  });
