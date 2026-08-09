import { Center, Loader, Menu, NavLink } from "@mantine/core";
import { IconLayoutDashboard, IconPackage, IconSettings } from "@tabler/icons-react";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { createRootRouteWithContext, Link, Outlet, redirect, useRouterState } from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import { AppShellLayout, appUrl, SpotlightSearchBox } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import { fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signOut } from "../api/auth";
import { AppSpotlight } from "../components/app-spotlight";
import { isPublicRoute } from "../lib/public-routes";
import { shellApps } from "../lib/shell-apps";

const navItems = [
  { label: "Dashboard", to: "/", icon: IconLayoutDashboard },
  { label: "Products", to: "/products", icon: IconPackage },
] as const;

const RootLayout = () => {
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const queryClient = useQueryClient();
  const publicRoute = isPublicRoute(pathname);
  const { data: session, isPending } = useQuery({
    queryKey: sessionQueryKey,
    queryFn: fetchSession,
    staleTime: 300_000,
    enabled: !publicRoute,
  });
  const logout = useMutation({
    mutationFn: signOut,
    onSuccess: () => {
      queryClient.setQueryData(sessionQueryKey, null);
      window.location.assign(appUrl("/sign-in"));
    },
  });
  const user = session?.user;
  const isOwner = user?.roles.includes("Owner") ?? false;

  useEffect(() => {
    if (!publicRoute && !isPending && !session) window.location.assign(appUrl("/sign-in"));
  }, [isPending, publicRoute, session]);

  // The public route deliberately bypasses the authenticated shell entirely.
  if (publicRoute) return <Outlet />;
  if (isPending || !session)
    return (
      <Center mih="100vh">
        <Loader size="sm" />
      </Center>
    );

  return (
    <>
      <AppShellLayout
        apps={shellApps}
        user={user}
        userMenuItems={
          isOwner && (
            <Menu.Item component={Link} to="/settings" leftSection={<IconSettings size={14} />}>
              Settings
            </Menu.Item>
          )
        }
        onSignOut={() => logout.mutate()}
        signOutDisabled={logout.isPending}
        navbarTop={<SpotlightSearchBox />}
        nav={(closeMobileNav) =>
          navItems.map((item) => (
            <NavLink
              key={item.to}
              component={Link}
              to={item.to}
              label={item.label}
              leftSection={<item.icon size={18} stroke={1.5} />}
              active={item.to === "/" ? pathname === "/" : pathname.startsWith(item.to)}
              onClick={closeMobileNav}
            />
          ))
        }
      >
        <Outlet />
      </AppShellLayout>

      <AppSpotlight />
      <TanStackRouterDevtools />
      <ReactQueryDevtools />
    </>
  );
};

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  beforeLoad: async ({ location, context }) => {
    if (isPublicRoute(location.pathname)) return;
    const session = await context.queryClient.fetchQuery({
      queryKey: sessionQueryKey,
      queryFn: fetchSession,
      staleTime: 300_000,
    });
    if (!session) {
      let bootstrapAvailable = false;
      try {
        bootstrapAvailable = (await fetchBootstrapStatus()).available;
      } catch {
        /* Setup status is optional; fall back to sign-in. */
      }
      if (bootstrapAvailable) throw redirect({ to: "/setup" });
      throw redirect({ to: "/sign-in" });
    }
  },
  component: RootLayout,
});
