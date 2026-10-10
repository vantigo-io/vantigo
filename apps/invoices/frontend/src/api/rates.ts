import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** One collection rate (invoices payments and reminders design D6), in force from its day until the next of its kind. */
export type CollectionRate = Schemas["InvoicesCollectionRate"];
export type CollectionRateKind = CollectionRate["kind"];
export type CollectionRateList = Schemas["InvoicesCollectionRateList"];
export type CollectionRateInput = Schemas["InvoicesCollectionRateRequest"];

/** The three kinds, in the order the card lists them. */
export const RATE_KINDS: readonly CollectionRateKind[] = [
  "late_interest_percent",
  "b2b_compensation_nok",
  "inkassosats",
];

/** Whether a kind is set per half-year, so a new row starts on 1 January or 1 July. */
export const halfYearly = (kind: CollectionRateKind): boolean => kind !== "inkassosats";

export const collectionRatesQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "collection-rates"],
    queryFn: ({ signal }) => request<CollectionRateList>("/api/v1/invoices/collection-rates", { signal }),
  });

/** Adds a rate ahead of a release (`invoices:manage`); a row of that kind and day is `collection_rate_exists`. */
export const addCollectionRate = (input: CollectionRateInput): Promise<CollectionRate> =>
  request<CollectionRate>("/api/v1/invoices/collection-rates", json("POST", input));

/** Deletes a rate not yet in force that no letter used (`invoices:manage`); otherwise `collection_rate_in_force`. */
export const deleteCollectionRate = (id: number): Promise<void> =>
  request<void>(`/api/v1/invoices/collection-rates/${id}`, { method: "DELETE" });
