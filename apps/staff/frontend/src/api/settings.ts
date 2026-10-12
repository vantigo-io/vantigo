import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

/**
 * The settings the deliveries so far expose (design §3.6) — A1's five and
 * A2's two retention periods — with the derived retention block and the
 * acknowledged thresholds, read-only; `staff:manage` reads and writes them.
 */
export type StaffSettings = Schemas["StaffSettings"];
export type StaffSettingsInput = Schemas["StaffSettingsRequest"];
export type StaffThresholdsAcknowledged = Schemas["StaffThresholdsAcknowledged"];
/** The four thresholds that bring an employer a duty (design S20), as the path names them; `50` means more than fifty. */
export type StaffThreshold = "5" | "30" | "50" | "otp";
export const staffThresholds: StaffThreshold[] = ["5", "30", "50", "otp"];

export const staffSettingsQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "settings"],
    queryFn: ({ signal }) => request<StaffSettings>("/api/v1/staff/settings", { signal }),
  });

/**
 * A full replace, guarded by the revision the form was opened at; an optional
 * field left out is cleared — but for the two retention periods, which a
 * body that leaves them out keeps.
 */
export const updateStaffSettings = (input: StaffSettingsInput, revision: number): Promise<StaffSettings> =>
  request<StaffSettings>("/api/v1/staff/settings", json("PUT", input, revision));

/**
 * Acknowledges a threshold, once and for good: its dashboard item never
 * comes back. Acknowledging it again changes nothing. Answers every
 * acknowledgement so far; the settings' revision does not move.
 */
export const acknowledgeThreshold = (key: StaffThreshold): Promise<StaffThresholdsAcknowledged> =>
  request<StaffThresholdsAcknowledged>(`/api/v1/staff/thresholds/${key}/acknowledge`, { method: "POST" });
