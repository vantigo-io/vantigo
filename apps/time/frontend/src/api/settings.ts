import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { request } from "./request";

type Schemas = components["schemas"];

/** `lockedBefore` is the first day still open: every day before it is locked. */
export type TimeSettings = Schemas["TimeSettingsResponse"];
export type TimeSettingsInput = Schemas["TimeSettingsRequest"];

/** Everyone may read the lock date; the week grid and the day view need it. */
export const timeSettingsQueryOptions = () =>
  queryOptions({
    queryKey: ["time", "settings"],
    queryFn: ({ signal }) => request<TimeSettings>("/api/v1/time/settings", { signal }),
  });

/** A null `lockedBefore` removes the lock. `time:manage` only. */
export const updateTimeSettings = (input: TimeSettingsInput): Promise<TimeSettings> =>
  request<TimeSettings>("/api/v1/time/settings", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/** Whether a day falls before the lock date. */
export const isLocked = (date: string, settings: TimeSettings | undefined): boolean =>
  Boolean(settings?.lockedBefore && date < settings.lockedBefore);
