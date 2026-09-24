import { type CustomerResponse, normalizeCustomer, type RawCustomerResponse } from "./customers";
import { type CsvDownload, downloadFile } from "./import-export";
import { request } from "./request";

/**
 * A private person's whole file (customers GDPR design D3), fetched the way the
 * customers file is — a plain `fetch`, so a refusal is read and shown rather
 * than navigated to — under the name the server attached it with.
 */
export const downloadPersonalData = (customer: Pick<CustomerResponse, "id" | "customerNumber">): Promise<CsvDownload> =>
  downloadFile(
    `/api/v1/customers/${customer.id}/personal-data`,
    `customer-${customer.customerNumber}-personal-data.json`,
  );

/**
 * Puts the anonymisation on `anonymiseOn` (yyyy-MM-dd, UTC), or moves it there
 * (GDPR design D4); answers the customer as it now is, its revision included. A
 * refusal is an `ApiConflictError` whose `code` says which —
 * `personal_data_not_a_person`, `personal_data_customer_active`,
 * `customer_merged`, `customer_anonymised` — and a bad day an
 * `ApiValidationError` on `anonymiseOn`.
 */
export const scheduleAnonymisation = async (customerId: number, anonymiseOn: string): Promise<CustomerResponse> =>
  normalizeCustomer(
    await request<RawCustomerResponse>(`/api/v1/customers/${customerId}/anonymisation`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ anonymiseOn }),
    }),
  );

/** Calls the anonymisation off; answers the customer as it now is. */
export const cancelAnonymisation = async (customerId: number): Promise<CustomerResponse> =>
  normalizeCustomer(
    await request<RawCustomerResponse>(`/api/v1/customers/${customerId}/anonymisation`, { method: "DELETE" }),
  );
