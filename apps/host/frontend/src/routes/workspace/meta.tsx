import { createFileRoute, redirect } from "@tanstack/react-router";
import { MetaSettingsPage } from "@vantigo/meta-ui";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { ModuleNotFound, moduleDisabledNotFound } from "../-app-layout";
import "../../i18n";

/**
 * Integrations › Meta (Messenger design N9): the Owner's page for the Meta app,
 * the webhook, the Human Agent switch and the Pages. The module has no
 * permission of its own, so the gate is: the signed-in user an Owner (anyone
 * else is sent home) and the meta module turned on (otherwise the not-enabled
 * page in place, as an app's layout does).
 */
export const Route = createFileRoute("/workspace/meta")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
    if (!enabledModuleKeys().includes("meta")) throw moduleDisabledNotFound("meta");
  },
  notFoundComponent: ({ data }) => <ModuleNotFound labelKey="navigation.meta" data={data} />,
  component: MetaSettingsPage,
});
