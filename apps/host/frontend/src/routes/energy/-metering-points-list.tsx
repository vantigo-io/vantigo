import { MeteringPointsPage } from "@vantigo/energy-ui/pages/metering-points.index";
import { hasPermissions } from "../../navigation";
import { useEnergyPermissions } from "./-energy-permissions";

// Each on its own: `hasPermissions` is satisfied by any one of the keys it is
// given, and the Import button wants both (energy import design D9). What the
// mapping may write is the import page's own question, asked of the other keys
// there.
export const IMPORT_PERMISSIONS = ["energy:import", "energy:metering-points-manage"] as const;

/**
 * The metering-point list, plus the capability it needs: `canImport`
 * (`energy:import` and `energy:metering-points-manage` together) puts
 * **Import** in the header, a link to the import page; `canViewTariffs`
 * (`energy:tariffs-view`) lets the new point's form list the grid tariffs.
 *
 * Lives beside the route file rather than inside it: a route file may export
 * nothing but its `Route` without costing the bundle a code split.
 */
export const MeteringPointsListPage = () => {
  const permissions = useEnergyPermissions();
  return (
    <MeteringPointsPage
      canImport={IMPORT_PERMISSIONS.every((permission) => hasPermissions(permissions, [permission]))}
      canViewTariffs={hasPermissions(permissions, ["energy:tariffs-view"])}
    />
  );
};
