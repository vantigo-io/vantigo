import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

/**
 * What the app may do, answered by the server rather than re-derived here
 * (design §5): the caller's capabilities, the business zone and its today,
 * and — for a caller holding `staff:view` — the company's structure, archived
 * rows included so a stored reference can still be named.
 */
export type StaffMeta = Schemas["StaffMeta"];
export type StaffCapabilities = Schemas["StaffCapabilities"];
export type StaffStructure = Schemas["StaffStructure"];

export const staffMetaQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "meta"],
    queryFn: ({ signal }) => request<StaffMeta>("/api/v1/staff/meta", { signal }),
  });
