import { PricesPage as EnergyPricesPage } from "@vantigo/energy-ui/pages/prices";
import { hasPermissions } from "../../navigation";
import { useEnergyPermissions } from "./-energy-permissions";

/**
 * The Prices page, plus the capability it needs: `canImport`
 * (`energy:prices-manage`) puts **Import prices** in the header (energy
 * consumption billing design D11).
 *
 * Lives beside the route file rather than inside it: a route file may export
 * nothing but its `Route` without costing the bundle a code split.
 */
export const PricesPage = () => {
  const permissions = useEnergyPermissions();
  return <EnergyPricesPage canImport={hasPermissions(permissions, ["energy:prices-manage"])} />;
};
