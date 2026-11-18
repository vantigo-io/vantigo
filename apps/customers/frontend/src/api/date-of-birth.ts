import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { request } from "./request";

/** A person's date of birth (invoices phase 6 design I30). */
export type CustomerDateOfBirth = components["schemas"]["CustomerDateOfBirth"];

export const dateOfBirthQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: ["customers", customerId, "date-of-birth"],
    queryFn: () => request<CustomerDateOfBirth>(`/api/v1/customers/${customerId}/date-of-birth`),
  });

/** Sets or clears the date; a business is refused 400 date_of_birth_person_only. */
export const putDateOfBirth = (customerId: number, dateOfBirth: string | null) =>
  request<CustomerDateOfBirth>(`/api/v1/customers/${customerId}/date-of-birth`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ dateOfBirth }),
  });
