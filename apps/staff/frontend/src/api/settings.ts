import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

/** The five settings delivery A1 exposes (design §3.6); `staff:manage` reads and writes them. */
export type StaffSettings = Schemas["StaffSettings"];
export type StaffSettingsInput = Schemas["StaffSettingsRequest"];

export const staffSettingsQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "settings"],
    queryFn: ({ signal }) => request<StaffSettings>("/api/v1/staff/settings", { signal }),
  });

/** A full replace, guarded by the revision the form was opened at; an optional field left out is cleared. */
export const updateStaffSettings = (input: StaffSettingsInput, revision: number): Promise<StaffSettings> =>
  request<StaffSettings>("/api/v1/staff/settings", json("PUT", input, revision));
