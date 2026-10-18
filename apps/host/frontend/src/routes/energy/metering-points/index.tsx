import { createFileRoute } from "@tanstack/react-router";
import { MeteringPointsListPage } from "../-metering-points-list";

export const Route = createFileRoute("/energy/metering-points/")({
  validateSearch: (search: Record<string, unknown>) => ({
    page: Math.max(1, Number(search.page) || 1),
    search: typeof search.search === "string" ? search.search : "",
    // Present only when true: the points with no customer now (energy import design D9).
    ...(search.withoutCustomer === true || search.withoutCustomer === "true" ? { withoutCustomer: true as const } : {}),
  }),
  component: MeteringPointsListPage,
});
