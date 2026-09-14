import { Alert, Center, Loader, Menu, NavLink, Stack, Text } from "@mantine/core";
import { IconSettings } from "@tabler/icons-react";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { createRootRouteWithContext, Link, Outlet, redirect, useRouterState } from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import { AppShellLayout, appUrl, SpotlightSearchBox, useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import "../i18n";
import { getProfile, profileQueryKey } from "../api/account";
import { fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signOut } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { fetchSystemStatus, shouldShowMaintenance, systemStatusQueryKey } from "../api/system-status";
import { AppSpotlight } from "../components/app-spotlight";
import { MaintenancePage } from "../components/errors";
import { ModuleAccessGuard } from "../components/module-access-guard";
import { publicPaths } from "../lib/public-paths";
import { activeNavPath, moduleKeys, type NavSection, visibleNavSections } from "../navigation";

const renderNavSections = (
  sections: readonly NavSection[],
  pathname: string,
  close: () => void,
  t: (key: string) => string,
) =>
  sections.map((section, sectionIndex) => {
    if (section.items.length === 0) return null;
    const active = activeNavPath(pathname, section.items);
    return (
      <div key={section.label ?? sectionIndex}>
        {section.label && (
          <Text size="xs" fw={700} tt="uppercase" c="dimmed" mt="md" mb={4} px="xs">
            {t(section.label)}
          </Text>
        )}
        {section.items.map((item) => (
          <NavLink
            key={item.to}
            component={Link}
            to={item.to}
            // Mantine styles [aria-current="page"] as active; keep TanStack's
            // own marker exact so only activeNavPath decides the highlight.
            activeOptions={{ exact: true }}
            label={t(item.label)}
            leftSection={<item.icon size={18} stroke={1.5} />}
            active={item.to === active}
            onClick={close}
          />
        ))}
      </div>
    );
  });

const RootLayout = () => {
  const { t } = useI18n("host");
  const location = useRouterState({ select: (state) => state.location });
  const pathname = location.pathname;
  const queryClient = useQueryClient();
  const [maintenanceWarningDismissed, setMaintenanceWarningDismissed] = useState(false);
  const isPublic = publicPaths.has(pathname);
  const { data: session, isPending } = useQuery({
    queryKey: sessionQueryKey,
    queryFn: fetchSession,
    enabled: !isPublic,
    staleTime: 300_000,
  });
  const systemStatus = useQuery({
    queryKey: systemStatusQueryKey,
    queryFn: fetchSystemStatus,
    staleTime: 30_000,
    refetchInterval: 30_000,
  });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !isPublic && !!session,
    retry: false,
    staleTime: 300_000,
  });
  const profile = useQuery({
    queryKey: profileQueryKey(session?.user.id ?? "unknown"),
    queryFn: getProfile,
    enabled: !isPublic && !!session,
    staleTime: 300_000,
  });
  const logout = useMutation({
    mutationFn: signOut,
    onSuccess: () => {
      queryClient.setQueryData(sessionQueryKey, null);
      window.location.assign(appUrl("/sign-in"));
    },
  });
  useEffect(() => {
    if (!isPublic && !isPending && !session) window.location.assign(appUrl("/sign-in"));
  }, [isPending, isPublic, session]);
  if (isPublic) return <Outlet />;
  if (isPending || !session)
    return (
      <Center mih="100vh">
        <Loader size="sm" />
      </Center>
    );
  const isOwner = session.user.roles.includes("Owner");
  const permissions = authorization.data?.permissions;
  const canManageAuthorization = authorization.data?.canManageAuthorization === true;
  // Module enablement used to be a per-tenant capability, fetched from the
  // deleted tenant-capabilities endpoint. Without tenants there is nothing to
  // vary: every module in the navigation catalog ships in this build, and
  // per-destination permissions still decide what a user actually sees.
  const enabledModules = moduleKeys;
  const visibleSections = visibleNavSections({
    permissions,
    isOwner,
    canManageAuthorization,
    isSystemAdmin: session.isSystemAdmin,
    enabledModules,
  });
  const primarySections = visibleSections.filter((section) => section.placement !== "lower");
  const lowerSections = visibleSections.filter((section) => section.placement === "lower");
  if (shouldShowMaintenance(systemStatus.data, session.isSystemAdmin)) {
    return <MaintenancePage message={systemStatus.data?.message} />;
  }
  return (
    <>
      <AppShellLayout
        moduleName="Vantigo"
        user={{
          ...session.user,
          avatarUrl: profile.data?.avatarUrl
            ? `${appUrl(profile.data.avatarUrl)}${profile.data.avatarUrl.includes("?") ? "&" : "?"}v=${profile.dataUpdatedAt}`
            : null,
        }}
        userMenuItems={
          <Menu.Item component={Link} to="/settings" leftSection={<IconSettings size={14} />}>
            {t("navigation.settings")}
          </Menu.Item>
        }
        onSignOut={() => logout.mutate()}
        signOutDisabled={logout.isPending}
        navbarTop={<SpotlightSearchBox />}
        nav={(close) => renderNavSections(primarySections, pathname, close, t)}
        navLower={(close) => renderNavSections(lowerSections, pathname, close, t)}
      >
        <Stack gap="md">
          {session.isSystemAdmin && systemStatus.data?.maintenance && !maintenanceWarningDismissed && (
            <Alert
              color="yellow"
              title={t("systemAdmin.maintenanceActive")}
              withCloseButton
              onClose={() => setMaintenanceWarningDismissed(true)}
            >
              {systemStatus.data.message || t("systemAdmin.maintenanceActiveBody")}
            </Alert>
          )}
          <ModuleAccessGuard>
            <Outlet />
          </ModuleAccessGuard>
        </Stack>
      </AppShellLayout>
      <AppSpotlight
        permissions={permissions}
        isOwner={isOwner}
        canManageAuthorization={canManageAuthorization}
        isSystemAdmin={session.isSystemAdmin}
        enabledModules={enabledModules}
      />
      <TanStackRouterDevtools />
      <ReactQueryDevtools />
    </>
  );
};

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  beforeLoad: async ({ location, context }) => {
    if (publicPaths.has(location.pathname)) return;
    const session = await context.queryClient.fetchQuery({
      queryKey: sessionQueryKey,
      queryFn: fetchSession,
      staleTime: 300_000,
    });
    if (!session) {
      let available = false;
      try {
        available = (await fetchBootstrapStatus()).available;
      } catch {
        /* optional */
      }
      throw redirect({ to: available ? "/setup" : "/sign-in" });
    }
    if (location.pathname === "/admin" || location.pathname.startsWith("/admin/")) {
      const legacyAdmin = location.pathname.match(/^\/admin\/(dashboard|users|invitations|roles)$/);
      if (!legacyAdmin && !session.isSystemAdmin) throw redirect({ to: "/" });
    }
  },
  component: RootLayout,
});
