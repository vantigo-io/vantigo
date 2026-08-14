import { Center, Loader, Menu, NavLink, Text } from "@mantine/core";
import { IconSettings } from "@tabler/icons-react";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { createRootRouteWithContext, Link, Outlet, redirect, useRouterState } from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import { AppShellLayout, appUrl, SpotlightSearchBox, useI18n } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import "../i18n";
import { getProfile, profileQueryKey } from "../api/account";
import { fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signOut } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { AppSpotlight } from "../components/app-spotlight";
import { activeNavPath, type NavSection, visibleNavSections } from "../navigation";

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
  const pathname = useRouterState({ select: (state) => state.location.pathname });
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
  const visibleSections = visibleNavSections(permissions, isOwner, canManageAuthorization);
  const primarySections = visibleSections.filter((section) => section.placement !== "lower");
  const lowerSections = visibleSections.filter((section) => section.placement === "lower");
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
        <Outlet />
      </AppShellLayout>
      <AppSpotlight permissions={permissions} isOwner={isOwner} canManageAuthorization={canManageAuthorization} />
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
  },
  component: RootLayout,
});
