import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { MANUFACTURING_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/** One location of an Inventory site (M4a). */
export type ManufacturingLocation = Schemas["ManufacturingLocation"];

/** Every location of a site, archived ones too, by full code; a select offers the active ones. */
export const siteLocationsQueryOptions = (siteId: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "sites", siteId, "locations"],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingLocationList"]>(`/api/v1/manufacturing/sites/${siteId}/locations`, {
        signal,
      }).then((answer) => answer.items),
    staleTime: 60_000,
  });
