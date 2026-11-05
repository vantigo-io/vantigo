import { createFileRoute, redirect } from "@tanstack/react-router";
import { ShippingSettingsPage } from "@vantigo/orders-ui/pages/shipping-settings";
import { getAuthorizationMe } from "../../api/authorization";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions } from "../../navigation";
import { ModuleNotFound, moduleDisabledNotFound } from "../-app-layout";

/**
 * Integrations › Shipping (orders design O51): the carrier accounts, their
 * keys and the default account per site. The page is Orders', so its gate
 * is: the orders module turned on (otherwise the not-enabled page in place)
 * and `orders:carriers-manage` (anyone else is sent home; the workspace
 * layout admitted them for another page).
 */
export const Route = createFileRoute("/workspace/shipping")({
  beforeLoad: async ({ context }) => {
    if (!enabledModuleKeys().includes("orders")) throw moduleDisabledNotFound("orders");
    const access = await context.queryClient.fetchQuery({
      queryKey: ["authorization", "me"],
      queryFn: getAuthorizationMe,
    });
    if (!hasPermissions(access.permissions, ["orders:carriers-manage"])) throw redirect({ to: "/" });
  },
  notFoundComponent: ({ data }) => <ModuleNotFound labelKey="navigation.shipping" data={data} />,
  component: ShippingSettingsPage,
});
