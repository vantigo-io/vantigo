import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { MANUFACTURING_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/** An active Inventory site, as Manufacturing reads it through contracts.Stock. */
export type ManufacturingSite = Schemas["ManufacturingSite"];

/** The active Inventory sites by code: where a work center can stand and an overview's availability is read. */
export const manufacturingSitesQueryOptions = () =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "sites"],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingSiteList"]>("/api/v1/manufacturing/sites", { signal }).then(
        (answer) => answer.items,
      ),
    staleTime: 60_000,
  });
