import { useParams } from "@tanstack/react-router";
import { ImportPage } from "@vantigo/energy-ui/pages/metering-points.import.$importId";
import { ImportStartPage } from "@vantigo/energy-ui/pages/metering-points.import.index";
import { hasPermissions } from "../../navigation";
import { useEnergyPermissions } from "./-energy-permissions";

/**
 * The metering-point import pages (energy import design D5, D9). The start
 * page needs nothing from the host but the route; the import page takes the
 * three capabilities its mapping step and attach panel show and hide by: the
 * create mode needs `energy:metering-points-manage` and `energy:meters-manage`
 * (a point is created with its first meter), the update mode
 * `energy:metering-points-manage`, and the customer link — mapping the
 * organisation number and the start, and attaching afterwards —
 * `energy:supply-periods-manage`. The door, `energy:import` with
 * `energy:metering-points-view`, is the server's rule on every operation; a
 * caller without it reaches these pages only by typing the address, and is
 * answered 403 by the first request.
 *
 * Lives beside the route files rather than inside them: a route file may
 * export nothing but its `Route` without costing the bundle a code split.
 */
const useCapabilities = () => {
  const permissions = useEnergyPermissions();
  const has = (key: string) => hasPermissions(permissions, [key]);
  return {
    canCreate: has("energy:metering-points-manage") && has("energy:meters-manage"),
    canUpdate: has("energy:metering-points-manage"),
    canLinkCustomers: has("energy:supply-periods-manage"),
  };
};

export const ImportStartRoutePage = () => <ImportStartPage />;

export const ImportRoutePage = () => {
  const { importId } = useParams({ from: "/energy/metering-points/import/$importId" });
  const capabilities = useCapabilities();
  return <ImportPage importId={importId} capabilities={capabilities} />;
};
