import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/**
 * The tag vocabulary (owner and tags design D2). `color` is one of Mantine's
 * named colours or null — the server validates it against exactly this list,
 * so a chip can pass it straight to Mantine's `color` prop without sanitising
 * anything.
 */
export const TAG_COLORS = [
  "gray",
  "red",
  "pink",
  "grape",
  "violet",
  "indigo",
  "blue",
  "cyan",
  "teal",
  "green",
  "lime",
  "yellow",
  "orange",
] as const;

export interface CustomerTag {
  id: string;
  name: string;
  color: string | null;
}

/** A tag with how many customers carry it — what the vocabulary list answers (design D3). */
export interface CustomerTagSummary extends CustomerTag {
  customerCount: number;
}

/**
 * `color` is nullable and omitted when unset, so a tag with no colour arrives
 * without the key at all. Absent and null mean the same thing, and this is the
 * one place that is decided — every component downstream reads
 * `color: string | null`, the same treatment `contactInfo` gets.
 */
type RawCustomerTag = Omit<CustomerTag, "color"> & { color?: string | null };
type RawCustomerTagSummary = RawCustomerTag & { customerCount: number };

export const normalizeTag = (raw: RawCustomerTag): CustomerTag => ({
  id: raw.id,
  name: raw.name,
  color: raw.color ?? null,
});

const normalizeTagSummary = (raw: RawCustomerTagSummary): CustomerTagSummary => ({
  ...normalizeTag(raw),
  customerCount: raw.customerCount,
});

/**
 * Every tag, name-ascending, with its customer count. One key for the whole
 * installation — a vocabulary is not paginated and not per-customer — and it
 * sits under the `["customers"]` prefix so every existing broad invalidation
 * refreshes it too.
 */
export const customerTagsQueryOptions = () =>
  queryOptions({
    queryKey: ["customers", "tags"],
    queryFn: async ({ signal }) =>
      (await request<RawCustomerTagSummary[]>("/api/v1/customers/tags", { signal })).map(normalizeTagSummary),
    placeholderData: keepPreviousData,
  });

export interface TagInput {
  name: string;
  color: string | null;
}

export const createTag = async (input: TagInput): Promise<CustomerTagSummary> =>
  normalizeTagSummary(
    await request<RawCustomerTagSummary>("/api/v1/customers/tags", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }),
  );

export const updateTag = async (id: string, input: TagInput): Promise<CustomerTagSummary> =>
  normalizeTagSummary(
    await request<RawCustomerTagSummary>(`/api/v1/customers/tags/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }),
  );

export const deleteTag = (id: string) => request<void>(`/api/v1/customers/tags/${id}`, { method: "DELETE" });

/**
 * Replaces a customer's tag set (design D2). There is no revision: tags are
 * off the customer row, so this write bumps nothing and needs no optimistic
 * token — two concurrent replaces are last-wins, which is what replacing a set
 * means.
 */
export const setCustomerTags = async (customerId: number, tagIds: string[]): Promise<{ tags: CustomerTag[] }> => {
  const answered = await request<{ tags: RawCustomerTag[] }>(`/api/v1/customers/${customerId}/tags`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tagIds }),
  });
  return { tags: answered.tags.map(normalizeTag) };
};
