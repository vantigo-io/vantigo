import { queryOptions } from "@tanstack/react-query";
import { json, ORDERS_QUERY_KEY, request, send } from "./request";
import type { Settings, SettingsInput } from "./types";

const url = "/api/v1/orders/settings";

/** Orders' settings (§9): the series, validity, payment terms, the send channel and the mail texts. */
export const settingsQueryOptions = () =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "settings"],
    queryFn: ({ signal }) => request<Settings>(url, { signal }),
  });

export const updateSettings = (input: SettingsInput) => send<Settings>(url, json("PUT", input));
