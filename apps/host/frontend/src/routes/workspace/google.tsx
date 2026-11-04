import { Stack } from "@mantine/core";
import { createFileRoute, Outlet, redirect, useNavigate, useRouterState } from "@tanstack/react-router";
import { PageHeader, PageTabs, useI18n } from "@vantigo/frontend-shell";
import { googleTabForPath, googleTabPath, googleTabs } from "@vantigo/google-ui";
import { getAuthorizationMe } from "../../api/authorization";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions } from "../../navigation";
import { ModuleNotFound, moduleDisabledNotFound } from "../-app-layout";
import "../../i18n";

/**
 * Integrations › Google Workspace (Google Workspace design G16): the Google
 * page, one header and four tabs, each a child route so a tab is a link. The
 * page is the module's, so its gate is: the module turned on (otherwise the
 * not-enabled page in place, as an app's layout does) and `google:manage`
 * (anyone else is sent home; the workspace layout admitted them for another
 * page). The Owner-only steps stay the Owner's inside the tabs; the tab bar
 * hides nothing by role.
 */
const GoogleLayout = () => {
  const { t } = useI18n("google");
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  return (
    <Stack gap="lg">
      <PageHeader title={t("google")} description={t("googleDescription")} />
      <PageTabs
        aria-label={t("google")}
        items={googleTabs.map((tab) => ({ value: tab.value, label: t(tab.labelKey) }))}
        value={googleTabForPath(pathname)}
        onChange={(tab) => void navigate({ to: googleTabPath(tab) as never })}
      />
      <Outlet />
    </Stack>
  );
};

export const Route = createFileRoute("/workspace/google")({
  beforeLoad: async ({ context }) => {
    if (!enabledModuleKeys().includes("google")) throw moduleDisabledNotFound("google");
    const access = await context.queryClient.fetchQuery({
      queryKey: ["authorization", "me"],
      queryFn: getAuthorizationMe,
    });
    if (!hasPermissions(access.permissions, ["google:manage"])) throw redirect({ to: "/" });
  },
  notFoundComponent: ({ data }) => <ModuleNotFound labelKey="navigation.google" data={data} />,
  component: GoogleLayout,
});
