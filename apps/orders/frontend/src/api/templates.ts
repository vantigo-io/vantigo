import { queryOptions } from "@tanstack/react-query";
import { json, ORDERS_QUERY_KEY, request, send } from "./request";
import type { Template, TemplateInput, TemplateSummary } from "./types";

const base = "/api/v1/orders/templates";

/** Every quote template by name (O23); the archived ones only when asked for. */
export const templateListQueryOptions = (includeArchived = false) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "templates", "list", includeArchived],
    queryFn: ({ signal }) =>
      request<{ items: TemplateSummary[] }>(`${base}${includeArchived ? "?includeArchived=true" : ""}`, {
        signal,
      }).then((answer) => answer.items),
  });

export const templateQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "templates", "detail", id],
    queryFn: ({ signal }) => request<Template>(`${base}/${id}`, { signal }),
  });

export const createTemplate = (input: TemplateInput) => send<Template>(base, json("POST", input));
export const updateTemplate = (id: number, input: TemplateInput) => send<Template>(`${base}/${id}`, json("PUT", input));
export const deleteTemplate = (id: number) => send<void>(`${base}/${id}`, { method: "DELETE" });
