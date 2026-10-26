import { getRouteApi } from "@tanstack/react-router";
import { PricesPage as EnergyPricesPage, type PricesSearch } from "@vantigo/energy-ui/pages/prices";
import { hasPermissions } from "../../navigation";
import { useEnergyPermissions } from "./-energy-permissions";

const route = getRouteApi("/energy/prices");

/**
 * The Prices page, plus what it needs from the host: `canImport`
 * (`energy:prices-manage`) puts **Import prices** in the header (energy
 * consumption billing design D11), and the view — the areas, the period,
 * its day or months — lives in the route's search parameters, so a view can
 * be linked and survives a reload (spot-price service design S8). A change
 * replaces the history entry rather than adding one.
 *
 * Lives beside the route file rather than inside it: a route file may export
 * nothing but its `Route` without costing the bundle a code split.
 */
export const PricesPage = () => {
  const permissions = useEnergyPermissions();
  const { areas, ...search } = route.useSearch();
  const navigate = route.useNavigate();
  const onSearchChange = (next: PricesSearch) => {
    void navigate({
      search: {
        ...(next.areas && next.areas.length > 0 ? { areas: next.areas.join(",") } : {}),
        ...(next.period ? { period: next.period } : {}),
        ...(next.date ? { date: next.date } : {}),
        ...(next.from ? { from: next.from } : {}),
        ...(next.to ? { to: next.to } : {}),
      },
      replace: true,
    });
  };
  return (
    <EnergyPricesPage
      canImport={hasPermissions(permissions, ["energy:prices-manage"])}
      search={{ ...search, ...(areas ? { areas: areas.split(",") } : {}) }}
      onSearchChange={onSearchChange}
    />
  );
};
