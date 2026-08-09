import { NavLink, Text } from "@mantine/core";
import { IconAdjustments, IconInbox } from "@tabler/icons-react";
import type { QueryClient } from "@tanstack/react-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  Link,
  Outlet,
  redirect,
  useNavigate,
  useRouterState,
} from "@tanstack/react-router";
import { AppShellLayout } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import { fetchBootstrapStatus, fetchSession, sessionQueryKey, signOut } from "../api/auth";
import { setUnauthorizedHandler } from "../api/request";
import { shellApps } from "../lib/shell-apps";

function Shell() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: session } = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300000 });
  const user = session?.user;
  useEffect(() => {
    setUnauthorizedHandler(() => {
      queryClient.setQueryData(sessionQueryKey, null);
      void navigate({ to: "/sign-in" });
    });
    return () => setUnauthorizedHandler(undefined);
  }, [navigate, queryClient]);
  if (pathname === "/sign-in" || pathname === "/setup") return <Outlet />;
  return (
    <AppShellLayout
      apps={shellApps}
      user={user}
      onSignOut={() =>
        void signOut().then(() => {
          queryClient.setQueryData(sessionQueryKey, null);
          void navigate({ to: "/sign-in" });
        })
      }
      nav={(closeMobileNav) => (
        <>
          <Text tt="uppercase" size="xs" fw={700} c="dimmed" px="sm" mb="xs">
            Workspace
          </Text>
          <NavLink
            component={Link}
            to="/messages"
            label="Messages"
            leftSection={<IconInbox size={18} />}
            active={pathname.startsWith("/messages") && pathname !== "/messages/compose"}
            onClick={closeMobileNav}
          />
          <NavLink
            component={Link}
            to="/messages/compose"
            label="Compose"
            leftSection={<IconAdjustments size={18} />}
            active={pathname === "/messages/compose"}
            onClick={closeMobileNav}
          />
          {user?.roles.includes("Owner") && (
            <>
              <NavLink
                component={Link}
                to="/admin/mailboxes"
                label="Mailboxes"
                leftSection={<IconInbox size={18} />}
                active={pathname.startsWith("/admin/mailboxes")}
                onClick={closeMobileNav}
              />
              <NavLink
                component={Link}
                to="/admin/suppressions"
                label="Suppressions"
                leftSection={<IconAdjustments size={18} />}
                active={pathname.startsWith("/admin/suppressions")}
                onClick={closeMobileNav}
              />
            </>
          )}
          <NavLink
            component={Link}
            to="/settings"
            label="Service API"
            leftSection={<IconAdjustments size={18} />}
            active={pathname.startsWith("/settings")}
            onClick={closeMobileNav}
          />
        </>
      )}
    >
      <Outlet />
    </AppShellLayout>
  );
}
export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  beforeLoad: async ({ location, context }) => {
    if (location.pathname === "/sign-in" || location.pathname === "/setup") return;
    const session = await context.queryClient.ensureQueryData({
      queryKey: sessionQueryKey,
      queryFn: fetchSession,
      staleTime: 300000,
    });
    if (!session) {
      const status = await fetchBootstrapStatus();
      throw redirect({ to: status.available ? "/setup" : "/sign-in" });
    }
  },
  component: Shell,
});
