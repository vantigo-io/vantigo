import { Stack } from "@mantine/core";
import { createFileRoute, Outlet, redirect, useNavigate, useRouterState } from "@tanstack/react-router";
import { entraTabForPath, entraTabPath, entraTabs } from "@vantigo/entra-ui";
import { PageHeader, PageTabs, useI18n } from "@vantigo/frontend-shell";
import { getAuthorizationMe } from "../../api/authorization";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions } from "../../navigation";
import { ModuleNotFound, moduleDisabledNotFound } from "../-app-layout";
import "../../i18n";

/**
 * Integrations › Microsoft Entra ID (Integrations design T2, T3): the Entra
 * page, one header and four tabs, each a child route so a tab is a link. The
 * page is the module's, so its gate is: the module turned on (otherwise the
 * not-enabled page in place, as an app's layout does) and `entra:manage`
 * (anyone else is sent home; the workspace layout admitted them for another
 * page). The Owner-only steps stay the Owner's inside the tabs; the tab bar
 * hides nothing by role.
 */
const EntraLayout = () => {
  const { t } = useI18n("entra");
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  return (
    <Stack gap="lg">
      <PageHeader title={t("entra")} description={t("entraDescription")} />
      <PageTabs
        aria-label={t("entra")}
        items={entraTabs.map((tab) => ({ value: tab.value, label: t(tab.labelKey) }))}
        value={entraTabForPath(pathname)}
        onChange={(tab) => void navigate({ to: entraTabPath(tab) as never })}
      />
      <Outlet />
    </Stack>
  );
};

export const Route = createFileRoute("/workspace/entra")({
  beforeLoad: async ({ context }) => {
    if (!enabledModuleKeys().includes("entra")) throw moduleDisabledNotFound("entra");
    const access = await context.queryClient.fetchQuery({
      queryKey: ["authorization", "me"],
      queryFn: getAuthorizationMe,
    });
    if (!hasPermissions(access.permissions, ["entra:manage"])) throw redirect({ to: "/" });
  },
  notFoundComponent: ({ data }) => <ModuleNotFound labelKey="navigation.entra" data={data} />,
  component: EntraLayout,
});
