import { Alert, Center, Loader, Menu, NavLink, Stack, Text, Title } from "@mantine/core";
import { IconSettings } from "@tabler/icons-react";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import {
  createRootRouteWithContext,
  Link,
  Outlet,
  redirect,
  useNavigate,
  useRouterState,
} from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import { AppShellLayout, appUrl, SpotlightSearchBox, useI18n } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import "../i18n";
import { getProfile, profileQueryKey } from "../api/account";
import { fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signOut, switchTenant } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { setActiveTenantSlug } from "../api/request";
import { AppSpotlight } from "../components/app-spotlight";
import { activeNavPath, type NavSection, visibleNavSections } from "../navigation";
import { activeTenantForSession, legacyTenantPath } from "./-tenant-routing";

const publicPaths = new Set([
  "/sign-in",
  "/setup",
  "/forgot-password",
  "/reset-password",
  "/password-reset",
  "/accept-invitation",
  "/invitations/accept",
]);

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
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const isPublic = publicPaths.has(pathname);
  const { data: session, isPending } = useQuery({
    queryKey: sessionQueryKey,
    queryFn: fetchSession,
    enabled: !isPublic,
    staleTime: 300_000,
  });
  const authorization = useQuery({
    queryKey: ["authorization", "me"],
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
  const tenants = session.tenants ?? [];
  const requestedTenantSlug = tenants.find(
    (tenant) => pathname === `/${tenant.slug}` || pathname.startsWith(`/${tenant.slug}/`),
  )?.slug;
  const activeTenant =
    tenants.find((tenant) => tenant.slug === requestedTenantSlug) ??
    tenants.find((tenant) => tenant.id === session.activeTenantId) ??
    (tenants.length === 1 ? tenants[0] : undefined);
  const visibleSections = visibleNavSections(
    permissions,
    isOwner,
    canManageAuthorization,
    activeTenant?.slug,
    session.isSystemAdmin,
  );
  const primarySections = visibleSections.filter((section) => section.placement !== "lower");
  const lowerSections = visibleSections.filter((section) => section.placement === "lower");
  const tenantUnavailable =
    tenants.length === 0 ||
    !activeTenant ||
    (activeTenant.status && !["active", "enabled"].includes(activeTenant.status.toLowerCase()));
  setActiveTenantSlug(requestedTenantSlug);
  const handleTenantSwitch = async (tenant: { id: string; slug: string }) => {
    const updated = await switchTenant(tenant.id);
    queryClient.setQueryData(sessionQueryKey, updated);
    // Tenant-owned data must never flash from the previous workspace.
    await queryClient.resetQueries({ predicate: (query) => query.queryKey[0] !== "auth" });
    setActiveTenantSlug(tenant.slug);
    const currentSubPath =
      activeTenant?.slug && pathname.startsWith(`/${activeTenant.slug}`)
        ? pathname.slice(activeTenant.slug.length + 1) || "/"
        : "/";
    await navigate({
      href: `/${encodeURIComponent(tenant.slug)}${currentSubPath}${location.searchStr}${location.hash ? `#${location.hash}` : ""}`,
    });
  };
  if (tenantUnavailable)
    return (
      <Center mih="100vh" p="xl">
        <Stack align="center" maw={440} ta="center">
          <Title order={2}>{tenants.length === 0 ? t("tenantRequiredTitle") : t("tenantUnavailableTitle")}</Title>
          <Text c="dimmed">{tenants.length === 0 ? t("tenantRequiredBody") : t("tenantUnavailableBody")}</Text>
          <Alert color="gray" variant="light">
            {t("tenantContactAdmin")}
          </Alert>
        </Stack>
      </Center>
    );
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
        tenants={tenants}
        activeTenantId={session.activeTenantId}
        onTenantSwitch={handleTenantSwitch}
        navbarTop={<SpotlightSearchBox />}
        nav={(close) => renderNavSections(primarySections, pathname, close, t)}
        navLower={(close) => renderNavSections(lowerSections, pathname, close, t)}
      >
        <Outlet />
      </AppShellLayout>
      <AppSpotlight
        permissions={permissions}
        isOwner={isOwner}
        canManageAuthorization={canManageAuthorization}
        isSystemAdmin={session.isSystemAdmin}
        tenantSlug={activeTenant?.slug}
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
    const legacyPrefixes = ["/customers", "/contacts", "/inbox", "/communications", "/products", "/energy"];
    const legacyAdmin = location.pathname.match(/^\/admin\/(dashboard|users|invitations|roles)$/);
    if (legacyAdmin) {
      const activeTenant = activeTenantForSession(session);
      if (activeTenant)
        throw redirect({
          href: `/${encodeURIComponent(activeTenant.slug)}/settings/${legacyAdmin[1] === "dashboard" ? "overview" : legacyAdmin[1]}`,
          replace: true,
        });
    }
    const legacyPrefix = legacyPrefixes.find(
      (prefix) => location.pathname === prefix || location.pathname.startsWith(`${prefix}/`),
    );
    if (legacyPrefix) {
      const activeTenant = activeTenantForSession(session);
      const href = legacyTenantPath(location.pathname, location.searchStr, location.hash, activeTenant?.slug);
      if (href) throw redirect({ href, replace: true });
    }
  },
  component: RootLayout,
});
