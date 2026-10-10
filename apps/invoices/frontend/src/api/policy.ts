import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** Whether a customer is reminded and charged (invoices payments and reminders design D7); no policy is normal. */
export type ReminderPolicy = Schemas["InvoicesReminderPolicy"];
export type ReminderPolicyInput = Schemas["InvoicesReminderPolicyRequest"];
export type ReminderPolicyMode = ReminderPolicy["mode"];

/** The three modes, from the least strict. */
export const POLICY_MODES: readonly ReminderPolicyMode[] = ["normal", "no_charges", "none"];

/** The customer's policy (`invoices:access`); one without a policy answers normal. */
export const reminderPolicyQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "reminder-policy", customerId],
    queryFn: ({ signal }) =>
      request<ReminderPolicy>(`/api/v1/invoices/customers/${customerId}/reminder-policy`, { signal }),
  });

/**
 * Sets the customer's policy (`invoices:payments`). A customer with no
 * document here, or anonymised, is a 404 — the shared client's
 * `NotFoundError`. Normal with an empty note removes the policy.
 */
export const setReminderPolicy = (customerId: number, input: ReminderPolicyInput): Promise<ReminderPolicy> =>
  request<ReminderPolicy>(`/api/v1/invoices/customers/${customerId}/reminder-policy`, json("PUT", input));
