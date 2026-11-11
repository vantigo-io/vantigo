import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { MANUFACTURING_QUERY_KEY, queryString, request } from "./request";

type Schemas = components["schemas"];

/** A user as Manufacturing names one: an order's responsible. */
export type ManufacturingPerson = Schemas["ManufacturingPerson"];

/** How many people a picker asks for at a time; the server answers at most 50. */
export const PEOPLE_SEARCH_LIMIT = 20;

/** Users whose display name matches `q` (`GET /manufacturing/people`), by display name. */
export const peopleSearchQueryOptions = (q: string, limit = PEOPLE_SEARCH_LIMIT) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "people", q, limit],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingPersonList"]>(`/api/v1/manufacturing/people${queryString({ q, limit })}`, {
        signal,
      }).then((answer) => answer.items),
  });
