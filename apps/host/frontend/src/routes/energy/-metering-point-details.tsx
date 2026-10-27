import { MeteringPointDetailsPage } from "@vantigo/energy-ui";
import { hasAllPermissions, hasPermissions } from "../../navigation";
import { useEnergyPermissions } from "./-energy-permissions";

/** What Import readings needs, all three (energy import design D5, D8). */
export const IMPORT_READINGS_PERMISSIONS = [
  "energy:import",
  "energy:consumption-manage",
  "energy:consumption-view",
] as const;

/** What the Settlements card needs, both (energy consumption billing design D5, D11). */
export const SETTLEMENTS_PERMISSIONS = ["energy:supply-periods-view", "energy:consumption-view"] as const;

/**
 * The metering point page, plus the capability it needs: `canImportReadings`
 * (`energy:import`, `energy:consumption-manage` and `energy:consumption-view`
 * together) puts **Import readings** in the Consumption card, and
 * `canViewTariffs` (`energy:tariffs-view`) lets the edit form and the
 * supply-period dialogs list the tariffs to choose from, and `canViewSettlements`
 * (`energy:supply-periods-view` and `energy:consumption-view` together) shows the
 * Settlements card.
 *
 * Lives beside the route file rather than inside it: a route file may export
 * nothing but its `Route` without costing the bundle a code split.
 */
export const MeteringPointDetailsRoutePage = () => {
  const permissions = useEnergyPermissions();
  return (
    <MeteringPointDetailsPage
      canImportReadings={hasAllPermissions(permissions, IMPORT_READINGS_PERMISSIONS)}
      canViewTariffs={hasPermissions(permissions, ["energy:tariffs-view"])}
      canViewSettlements={hasAllPermissions(permissions, SETTLEMENTS_PERMISSIONS)}
    />
  );
};
