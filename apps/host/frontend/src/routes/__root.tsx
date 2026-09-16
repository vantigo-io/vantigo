import { Alert, Center, Loader, NavLink, Stack, Text } from "@mantine/core";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import {
  createRootRouteWithContext,
  Link,
  Outlet,
  redirect,
  useMatches,
  useNavigate,
  useRouterState,
} from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import {
  AccountMenu,
  AppShellLayout,
  AppSwitcher,
  appUrl,
  SpotlightSearchBox,
  SpotlightSearchButton,
  useI18n,
} from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import "../i18n";
import { accountMenuSections } from "../account-menu";
import { getProfile, profileQueryKey } from "../api/account";
import { fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signOut } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { fetchSystemStatus, shouldShowMaintenance, systemStatusQueryKey } from "../api/system-status";
import { activeAppKey, appForKey, appNavSections, appTitleLabel, switcherTiles } from "../apps";
import { AppSpotlight } from "../components/app-spotlight";
import { MaintenancePage } from "../components/errors";
import { ModuleAccessGuard } from "../components/module-access-guard";
import { enabledModuleKeys } from "../lib/enabled-modules";
import { publicPaths } from "../lib/public-paths";
import { activeNavPath, type NavSection, visibleNavSections } from "../navigation";

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
  const matches = useMatches();
  const navigate = useNavigate();
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
  const enabledModules = enabledModuleKeys();
  const visibility = {
    permissions,
    isOwner,
    canManageAuthorization,
    isSystemAdmin: session.isSystemAdmin,
    enabledModules,
  };
  // The app is whatever the deepest matched route declares; administration
  // and public paths declare none and render sidebar-less.
  const activeKey = activeAppKey(matches);
  const activeApp = activeKey ? appForKey(activeKey) : undefined;
  const navSections = appNavSections(activeApp, enabledModules, visibility);
  const titleLabel = appTitleLabel(activeApp);
  const menuSections = visibleNavSections(accountMenuSections, visibility);
  // Sidebar links and the registry use bare path strings, as the nav catalog
  // always has; the router validates search params at runtime.
  const go = (to: string) => void navigate({ to: to as never });
  if (shouldShowMaintenance(systemStatus.data, session.isSystemAdmin)) {
    return <MaintenancePage message={systemStatus.data?.message} />;
  }
  return (
    <>
      <AppShellLayout
        title={titleLabel ? t(titleLabel) : undefined}
        headerCenter={<SpotlightSearchBox />}
        headerActions={
          <>
            <SpotlightSearchButton />
            <AppSwitcher
              apps={switcherTiles(permissions, enabledModules, activeKey).map((tile) => ({
                id: tile.app.key,
                label: t(tile.app.label),
                icon: tile.app.icon,
                current: tile.current,
                disabledReason: tile.enabled ? undefined : t("navigation.notEnabled"),
                onSelect: () => go(tile.app.home),
              }))}
            />
            <AccountMenu
              user={{
                ...session.user,
                avatarUrl: profile.data?.avatarUrl
                  ? `${appUrl(profile.data.avatarUrl)}${profile.data.avatarUrl.includes("?") ? "&" : "?"}v=${profile.dataUpdatedAt}`
                  : null,
              }}
              sections={menuSections.map((section) => ({
                label: t(section.label),
                items: section.items.map((item) => ({
                  label: t(item.label),
                  icon: item.icon,
                  onSelect: () => go(item.to),
                })),
              }))}
              onSignOut={() => logout.mutate()}
              signOutDisabled={logout.isPending}
            />
          </>
        }
        nav={navSections.length > 0 ? (close) => renderNavSections(navSections, pathname, close, t) : undefined}
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
    if ((location.pathname === "/admin" || location.pathname.startsWith("/admin/")) && !session.isSystemAdmin) {
      throw redirect({ to: "/" });
    }
  },
  component: RootLayout,
});
