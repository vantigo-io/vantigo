import { type CustomerResponse, normalizeCustomer, type RawCustomerResponse } from "./customers";
import { request } from "./request";

/** One kind of reference a merge moved to the surviving customer, and how many (merge design D3). */
export interface CustomerMergeMove {
  kind: string;
  count: number;
}

/** What `POST /customers/{id}/merge` answers: the survivor as it is now, and every kind that moved to it. */
export interface CustomerMergeResult {
  customer: CustomerResponse;
  moved: CustomerMergeMove[];
}

export interface CustomerMergeInput {
  /** The customer to absorb into the one merged into. */
  sourceId: number;
  /** The surviving customer's revision, so a merge from a stale page is refused (design D5's rule). */
  revision?: number;
}

/**
 * The customer `id` absorbs `input.sourceId` (merge design D2). A refusal is an
 * `ApiConflictError` whose `code` says which — `merge_self`,
 * `merge_type_mismatch`, `merge_into_archived`, `merge_already_merged` — or
 * that has none, for a stale revision.
 */
export const mergeCustomer = async (id: number, input: CustomerMergeInput): Promise<CustomerMergeResult> => {
  const answered = await request<{ customer: RawCustomerResponse; moved: CustomerMergeMove[] }>(
    `/api/v1/customers/${id}/merge`,
    { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(input) },
  );
  return { customer: normalizeCustomer(answered.customer), moved: answered.moved };
};
