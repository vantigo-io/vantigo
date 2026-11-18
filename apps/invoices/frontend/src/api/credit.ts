import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { BankTransaction } from "./bank";
import type { InvoiceDocument } from "./invoices";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** A customer's credit (invoices phase 6 design I8, I12): its balances, a page of its movements, its refunds. */
export type CustomerCredit = Schemas["InvoicesCustomerCredit"];
export type CreditMovement = Schemas["InvoicesCreditMovement"];
export type CreditRefund = Schemas["InvoicesCreditRefund"];
export type CreditRefundInput = Schemas["InvoicesCreditRefundRequest"];
export type ApplyCreditInput = Schemas["InvoicesApplyCreditRequest"];
export type KeepAsCreditInput = Schemas["InvoicesKeepAsCreditRequest"];

/** The customer's credit (`invoices:access`); a customer with none answers no balance. */
export const customerCreditQueryOptions = (customerId: number, page = 1) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "customer-credit", customerId, page],
    queryFn: ({ signal }) =>
      request<CustomerCredit>(`/api/v1/invoices/customers/${customerId}/credit?page=${page}`, { signal }),
  });

/** The customer's credit in currency, 0 with none. */
export const balanceIn = (credit: CustomerCredit | undefined, currency: string): number =>
  credit?.balances.find((b) => b.currency === currency)?.balance ?? 0;

/** Applies the customer's credit to an issued invoice (I11). The caller invalidates the document. */
export const applyCredit = (id: number, input: ApplyCreditInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/apply-credit`, json("POST", input));

/** Records money paid back outside Vantigo (I12); the answer is the customer's credit. */
export const recordRefund = (customerId: number, input: CreditRefundInput): Promise<CustomerCredit> =>
  request<CustomerCredit>(`/api/v1/invoices/customers/${customerId}/credit/refunds`, json("POST", input));

/** Removes a refund with a reason; the credit comes back. */
export const removeRefund = (customerId: number, refundId: number, reason: string): Promise<CustomerCredit> =>
  request<CustomerCredit>(
    `/api/v1/invoices/customers/${customerId}/credit/refunds/${refundId}/remove`,
    json("POST", { reason }),
  );

/** Keeps a queued line's surplus as the invoice's customer's credit (I9). */
export const keepAsCredit = (id: number, input: KeepAsCreditInput): Promise<BankTransaction> =>
  request<BankTransaction>(`/api/v1/invoices/bank-transactions/${id}/keep-as-credit`, json("POST", input));
