import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, PURCHASING_QUERY_KEY, queryString, request, send } from "./request";

type Schemas = components["schemas"];

/** The proposals' shapes (purchasing design P31–P33, A4), under the names the page uses. */
export type PurchasingProposal = Schemas["PurchasingProposal"];
export type PurchasingProposalList = Schemas["PurchasingProposalList"];
export type PurchasingProposalOption = Schemas["PurchasingProposalOption"];
export type PurchasingProposalOrdersInput = Schemas["PurchasingProposalOrdersInput"];
export type PurchasingProposalOrdersRow = Schemas["PurchasingProposalOrdersRow"];
export type PurchasingProposalOrdersResult = Schemas["PurchasingProposalOrdersResult"];

const BASE = "/api/v1/purchasing/proposals";

/** The proposals' filters: one site, one variant (the links in from low stock and a buy suggestion, P35). */
export interface ProposalFilters {
  siteId?: number;
  variantId?: number;
}

/** The proposals, as the demand's one source answers them now. */
export const proposalsQueryOptions = (filters: ProposalFilters) =>
  queryOptions({
    queryKey: [PURCHASING_QUERY_KEY, "proposals", filters],
    queryFn: ({ signal }) =>
      request<PurchasingProposalList>(
        `${BASE}${queryString({ siteId: filters.siteId, variantId: filters.variantId })}`,
        { signal },
      ),
  });

/** Create orders (P33, A11): one draft per supplier and site; a replay of the id answers the first result. */
export const createOrdersFromProposals = (input: PurchasingProposalOrdersInput) =>
  send<PurchasingProposalOrdersResult>(`${BASE}/orders`, json("POST", input));
