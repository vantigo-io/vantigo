import { createFileRoute } from "@tanstack/react-router";
import { MeteringPointsPage } from "@vantigo/energy-ui/pages/metering-points.index";

export const Route = createFileRoute("/energy/metering-points/")({
  validateSearch: (search: Record<string, unknown>) => ({
    page: Math.max(1, Number(search.page) || 1),
    search: typeof search.search === "string" ? search.search : "",
  }),
  component: MeteringPointsPage,
});
