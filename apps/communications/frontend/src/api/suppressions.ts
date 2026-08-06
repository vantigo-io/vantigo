import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export interface Suppression {
  id: string;
  emailAddress: string;
  reason: string | null;
  createdAt: string;
}

export interface CreateSuppressionRequest {
  emailAddress: string;
  reason?: string;
}

export const suppressionsQueryOptions = () =>
  queryOptions({
    queryKey: ["suppressions"],
    queryFn: ({ signal }) => request<Suppression[]>("/api/v1/suppressions", { signal }),
  });

export const createSuppression = (body: CreateSuppressionRequest) =>
  request<Suppression>("/api/v1/suppressions", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

export const deleteSuppression = (id: string) =>
  request<void>(`/api/v1/suppressions/${encodeURIComponent(id)}`, { method: "DELETE" });
