import { IconLayoutDashboard, IconShieldCheck, IconUserPlus, IconUsers } from "@tabler/icons-react";
import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { SettingsLayout } from "../components/settings-layout";
import "../i18n";

export const Route = createFileRoute("/workspace")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
  },
  component: TenantSettings,
});

function TenantSettings() {
  const { t } = useI18n("host");
  return (
    <SettingsLayout
      title={t("systemAdmin.settings")}
      description={t("systemAdmin.manageWorkspace")}
      sections={[
        { label: t("systemAdmin.overview"), to: "/workspace/overview", icon: IconLayoutDashboard },
        { label: t("systemAdmin.users"), to: "/workspace/users", icon: IconUsers },
        { label: t("systemAdmin.invitations"), to: "/workspace/invitations", icon: IconUserPlus },
        { label: t("systemAdmin.roles"), to: "/workspace/roles", icon: IconShieldCheck },
      ]}
    >
      <Outlet />
    </SettingsLayout>
  );
}
