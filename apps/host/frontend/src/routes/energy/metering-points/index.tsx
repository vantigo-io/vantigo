import { createFileRoute } from "@tanstack/react-router";
import { CONNECTION_STATUSES, METERING_POINT_SORTS, PRICE_AREAS } from "@vantigo/energy-ui/api/energy";
import type { MeteringPointsSearch } from "@vantigo/energy-ui/pages/metering-points.index";
import { MeteringPointsListPage } from "../-metering-points-list";

/** A value from a known set, or no filter at all — never an unknown value forwarded to the API. */
const oneOf = <T extends string>(values: readonly T[], value: unknown): T | undefined =>
  values.includes(value as T) ? (value as T) : undefined;

export const Route = createFileRoute("/energy/metering-points/")({
  // The list reads these back with useSearch, so every filter and the sort
  // survive a refresh and a pasted link; an unknown one is no filter rather
  // than a 400 from the API.
  validateSearch: (search: Record<string, unknown>): MeteringPointsSearch => {
    const customerId = Number(search.customerId);
    return {
      page: Math.max(1, Number(search.page) || 1),
      search: typeof search.search === "string" ? search.search : "",
      // Present only when true: the points with no customer now (energy import design D9).
      ...(search.withoutCustomer === true || search.withoutCustomer === "true" ? { withoutCustomer: true } : {}),
      customerId: Number.isInteger(customerId) && customerId > 0 ? customerId : undefined,
      priceArea: oneOf(PRICE_AREAS, search.priceArea),
      connectionStatus: oneOf(CONNECTION_STATUSES, search.connectionStatus),
      sortBy: oneOf(METERING_POINT_SORTS, search.sortBy),
      sortDirection: oneOf(["asc", "desc"] as const, search.sortDirection),
    };
  },
  component: MeteringPointsListPage,
});
