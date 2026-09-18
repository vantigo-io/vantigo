import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { request } from "./request";

type Schemas = components["schemas"];

/** A person's bill and cost rate from a date on, until the next card starts. `time:manage` only. */
export type PersonRate = Schemas["TimeRateResponse"];
export type PersonRateInput = Schemas["TimeRateRequest"];
export type PersonRateUpdateInput = Schemas["TimeRateUpdateRequest"];

/** Every rate card, or one person's when a user id is given. */
export const personRatesQueryOptions = (userId?: string) =>
  queryOptions({
    queryKey: ["time", "rates", userId ?? "all"],
    queryFn: ({ signal }) =>
      request<PersonRate[]>(userId ? `/api/v1/time/rates/users/${userId}` : "/api/v1/time/rates", { signal }),
  });

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export const createPersonRate = (input: PersonRateInput): Promise<PersonRate> =>
  request<PersonRate>("/api/v1/time/rates", json("POST", input));

export const updatePersonRate = (id: number, input: PersonRateUpdateInput): Promise<PersonRate> =>
  request<PersonRate>(`/api/v1/time/rates/${id}`, json("PUT", input));

export const deletePersonRate = (id: number): Promise<void> =>
  request<void>(`/api/v1/time/rates/${id}`, { method: "DELETE" });
