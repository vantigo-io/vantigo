import { TariffsPage } from "@vantigo/energy-ui/pages/tariffs";
import { hasPermissions } from "../../navigation";
import { useEnergyPermissions } from "./-energy-permissions";

/**
 * The Tariffs page, plus what it needs from the host: `canManage`
 * (`energy:tariffs-manage`) puts **New tariff** in the header and Edit,
 * Archive and Delete on each row (energy consumption billing design D11).
 *
 * Lives beside the route file rather than inside it: a route file may export
 * nothing but its `Route` without costing the bundle a code split.
 */
export const TariffsRoutePage = () => {
  const permissions = useEnergyPermissions();
  return <TariffsPage canManage={hasPermissions(permissions, ["energy:tariffs-manage"])} />;
};
