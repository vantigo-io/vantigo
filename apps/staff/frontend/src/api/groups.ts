import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

/**
 * Groups (Entra design D9, D10): listed (HR names the members) or derived (a
 * rule over the register, materialised by the accounts worker on the
 * business day), with owners, and a section per provider in the slot that
 * carries the traits the provider declares. `staff:manage`'s alone.
 */
export type StaffGroup = Schemas["StaffGroup"];
export type StaffGroupInput = Schemas["StaffGroupRequest"];
export type StaffGroupKind = Schemas["StaffGroupKind"];
export type StaffGroupRule = Schemas["StaffGroupRule"];
export type StaffGroupRuleKind = StaffGroupRule["kind"];
export type StaffGroupMember = Schemas["StaffGroupMember"];
export type StaffGroupOwner = Schemas["StaffGroupOwner"];
export type StaffGroupProvider = Schemas["StaffGroupProvider"];
export type StaffGroupProviderState = Schemas["StaffGroupProviderState"];
export type StaffGroupTraitDeclaration = Schemas["StaffGroupTraitDeclaration"];
export type StaffGroupTraitText = Schemas["StaffGroupTraitText"];
export type StaffGroupTraitsInput = Schemas["StaffGroupTraitsRequest"];
export type StaffGroupSyncState = Schemas["StaffGroupSyncState"];
export type StaffPersonGroup = Schemas["StaffPersonGroup"];

/** The rule's kinds, in the order the form offers them. */
export const groupRuleKinds = [
  "employed",
  "department",
  "businessUnit",
  "location",
  "employeeType",
  "reportsTo",
] as const satisfies readonly StaffGroupRuleKind[];

/** The rule's kinds that name a row by `id`: a department, a unit, a location, a person. */
export const groupRuleKindsById: readonly StaffGroupRuleKind[] = [
  "department",
  "businessUnit",
  "location",
  "reportsTo",
];

/** A group's key: lower-case letters, digits and hyphens, starting with a letter or a digit, at most 60. */
export const GROUP_KEY_PATTERN = /^[a-z0-9][a-z0-9-]{0,59}$/;

const base = "/api/v1/staff/groups";

/** Every group, archived ones included, without the members and owners themselves. */
export const groupsQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "groups"],
    queryFn: ({ signal }) => request<StaffGroup[]>(base, { signal }),
  });

/** One group with its members and owners by name. */
export const groupQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "groups", id],
    queryFn: ({ signal }) => request<StaffGroup>(`${base}/${id}`, { signal }),
  });

/** The group's section under every provider in the slot: the declaration, the values, where the sync stands. */
export const groupTraitsQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "groups", id, "traits"],
    queryFn: ({ signal }) => request<Schemas["StaffGroupTraits"]>(`${base}/${id}/traits`, { signal }),
  });

/** The caller's own groups by name (`GET /me/groups`); `no_person` (404) for a caller linked to nobody. */
export const myGroupsQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "me", "groups"],
    queryFn: ({ signal }) => request<StaffPersonGroup[]>("/api/v1/staff/me/groups", { signal }),
    retry: false,
  });

export const createGroup = (input: StaffGroupInput) => request<StaffGroup>(base, json("POST", input));

/** A full replace under the revision read; the key never changes. 409 `group_archived` on an archived group. */
export const updateGroup = (id: number, input: StaffGroupInput, revision: number) =>
  request<StaffGroup>(`${base}/${id}`, json("PUT", input, revision));

/** Idempotent; the group keeps its members and becomes read-only. */
export const archiveGroup = (id: number) => request<StaffGroup>(`${base}/${id}/archive`, { method: "POST" });

/** Only a group no provider ever made or found; else 409 `group_synced` — archive it instead. */
export const deleteGroup = (id: number) => request<void>(`${base}/${id}`, { method: "DELETE" });

/** A listed group's members, exactly these people from today; 409 `group_derived` for a derived group. */
export const setGroupMembers = (id: number, personIds: number[]) =>
  request<StaffGroup>(`${base}/${id}/members`, json("PUT", { personIds }));

export const setGroupOwners = (id: number, personIds: number[]) =>
  request<StaffGroup>(`${base}/${id}/owners`, json("PUT", { personIds }));

/** A provider on with the values (checked against its declaration, field errors keyed by trait), or off. */
export const setGroupTraits = (id: number, provider: string, input: StaffGroupTraitsInput) =>
  request<StaffGroupProvider>(`${base}/${id}/traits/${encodeURIComponent(provider)}`, json("PUT", input));
