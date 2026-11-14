import { createFileRoute, redirect } from "@tanstack/react-router";
import { BankFeedSettingsPage } from "@vantigo/invoices-ui/pages/bank-feed-settings";
import { getAuthorizationMe } from "../../api/authorization";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasAllPermissions } from "../../navigation";
import { ModuleNotFound, moduleDisabledNotFound } from "../-app-layout";

/**
 * Integrations › Bank (invoices bank feed design F9): the bank connection,
 * its API key, write-only and sealed, the bank's accounts with their switches
 * and start days, and Disconnect. The page is Invoices', so its gate is: the
 * invoices module turned on (otherwise the not-enabled page in place) and
 * `invoices:access` with `invoices:manage` (anyone else is sent home; the
 * workspace layout admitted them for another page).
 */
export const Route = createFileRoute("/workspace/bank")({
  beforeLoad: async ({ context }) => {
    if (!enabledModuleKeys().includes("invoices")) throw moduleDisabledNotFound("invoices");
    const access = await context.queryClient.fetchQuery({
      queryKey: ["authorization", "me"],
      queryFn: getAuthorizationMe,
    });
    if (!hasAllPermissions(access.permissions, ["invoices:access", "invoices:manage"])) throw redirect({ to: "/" });
  },
  notFoundComponent: ({ data }) => <ModuleNotFound labelKey="navigation.bank" data={data} />,
  component: BankFeedSettingsPage,
});
