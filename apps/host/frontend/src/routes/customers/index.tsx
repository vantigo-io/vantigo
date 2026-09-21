import { createFileRoute } from "@tanstack/react-router";
import {
  type CustomerStatusFilter,
  type CustomerType,
  customerStatsQueryOptions,
  customersListParams,
  customersQueryOptions,
} from "@vantigo/customers-ui/api/customers";
import { CustomersPage } from "@vantigo/customers-ui/pages/customers.index";

const CUSTOMER_STATUSES: readonly CustomerStatusFilter[] = ["active", "disabled", "archived"];
const CUSTOMER_TYPES: readonly CustomerType[] = ["business", "person"];
const SORT_FIELDS = ["id", "name", "customerNumber", "createdAt", "updatedAt"] as const;
const SORT_DIRECTIONS = ["asc", "desc"] as const;

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
    ]),
  component: CustomersPage,
});
