import { createFileRoute } from "@tanstack/react-router";
import {
  type CustomerStatusFilter,
  type CustomerType,
  customerStatsQueryOptions,
  customersListParams,
  customersQueryOptions,
} from "@vantigo/customers-ui/api/customers";
import { customerTagsQueryOptions } from "@vantigo/customers-ui/api/tags";
import { CustomersListPage } from "./-customers-list";

const CUSTOMER_STATUSES: readonly CustomerStatusFilter[] = ["active", "disabled", "archived"];
const CUSTOMER_TYPES: readonly CustomerType[] = ["business", "person"];
const SORT_FIELDS = ["id", "name", "customerNumber", "createdAt", "updatedAt"] as const;
const SORT_DIRECTIONS = ["asc", "desc"] as const;
const OWNER_FILTERS = ["me", "none"] as const;
/** A tag id is a uuid; anything else is no filter rather than a 400 from the API. */
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const ownerFilterOf = (value: unknown): string | undefined => {
  if (typeof value !== "string") return undefined;
  if ((OWNER_FILTERS as readonly string[]).includes(value)) return value;
  return UUID.test(value) ? value : undefined;
};
const tagFilterOf = (value: unknown): string | undefined =>
  typeof value === "string" && UUID.test(value) ? value : undefined;

/** A value from a known set, or no filter at all — never an unknown value forwarded to the API. */
const oneOf = <T extends string>(values: readonly T[], value: unknown): T | undefined =>
  values.includes(value as T) ? (value as T) : undefined;

export const Route = createFileRoute("/customers/")({
  // The list page reads these back with useSearch, so every filter and the
  // sort survive a refresh and a pasted link. An unknown status, type or
  // sort falls back to no filter/no sort rather than reaching the API as a
  // value it would reject — the URL stays clean rather than carrying
  // `status=bogus`.
  validateSearch: (search: Record<string, unknown>) => ({
    page: Math.max(1, Number(search.page) || 1),
    search: typeof search.search === "string" ? search.search : "",
    status: oneOf(CUSTOMER_STATUSES, search.status),
    type: oneOf(CUSTOMER_TYPES, search.type),
    ownerId: ownerFilterOf(search.ownerId),
    tagId: tagFilterOf(search.tagId),
    sortBy: oneOf(SORT_FIELDS, search.sortBy),
    sortDirection: oneOf(SORT_DIRECTIONS, search.sortDirection),
    // Present only when true: the create form opens on arrival (Spotlight's quick action).
    ...(search.create === true || search.create === "true" ? { create: true as const } : {}),
  }),
  // Built through the same helper the page calls on its own useSearch value
  // (`customersListParams`), so the loader's prefetch and the page's
  // useQuery land on the identical query key and the page never re-fetches
  // what the loader already has.
  loaderDeps: ({ search }) => customersListParams(search),
  loader: ({ context: { queryClient }, deps }) =>
    Promise.all([
      queryClient.ensureQueryData(customersQueryOptions(deps)),
      queryClient.ensureQueryData(customerStatsQueryOptions()),
      queryClient.ensureQueryData(customerTagsQueryOptions()),
    ]),
  component: CustomersListPage,
});
