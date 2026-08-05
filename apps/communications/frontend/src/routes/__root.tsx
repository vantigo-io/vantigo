import { AppShell, Avatar, Burger, Divider, Group, Menu, NavLink, Text, Title, UnstyledButton } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { IconAdjustments, IconArrowUpRight, IconInbox, IconLogout, IconSelector } from "@tabler/icons-react";
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
import { useEffect } from "react";
import { fetchBootstrapStatus, fetchSession, sessionQueryKey, signOut } from "../api/auth";
import { setUnauthorizedHandler } from "../api/request";

const customersUrl = import.meta.env.VITE_CUSTOMERS_URL || "http://localhost:10011";
function Shell() {
  const [opened, { toggle, close }] = useDisclosure();
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
    <AppShell
      header={{ height: 72 }}
      navbar={{ width: 260, breakpoint: "sm", collapsed: { mobile: !opened } }}
      padding="xl"
    >
      <AppShell.Header>
        <Group h="100%" px="xl">
          <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" />
          <div className="brand-mark">✦</div>
          <Divider orientation="vertical" my="lg" />
          <div>
            <Title order={4}>Communications</Title>
            <Text size="xs" c="dimmed">
              Message operations
            </Text>
          </div>
          <div style={{ marginLeft: "auto" }}>
            <Menu withArrow>
              <Menu.Target>
                <UnstyledButton>
                  <Group gap="xs">
                    <Avatar size="sm" color="indigo">
                      {user?.displayName?.slice(0, 1) || "?"}
                    </Avatar>
                    <Text visibleFrom="sm" size="sm">
                      {user?.displayName || "Workspace"}
                    </Text>
                    <IconSelector size={15} />
                  </Group>
                </UnstyledButton>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item
                  leftSection={<IconLogout size={15} />}
                  onClick={() =>
                    void signOut().then(() => {
                      queryClient.setQueryData(sessionQueryKey, null);
                      void navigate({ to: "/sign-in" });
                    })
                  }
                >
                  Sign out
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
          </div>
        </Group>
      </AppShell.Header>
      <AppShell.Navbar p="md">
        <Text tt="uppercase" size="xs" fw={700} c="dimmed" px="sm" mb="xs">
          Workspace
        </Text>
        <NavLink
          component={Link}
          to="/messages"
          label="Message history"
          leftSection={<IconInbox size={18} />}
          active={pathname.startsWith("/messages")}
          onClick={close}
        />
        <NavLink
          component={Link}
          to="/settings"
          label="Service API"
          leftSection={<IconAdjustments size={18} />}
          active={pathname.startsWith("/settings")}
          onClick={close}
        />
        <AppShell.Section mt="auto">
          <Divider mb="md" />
          <Group p="sm">
            <div className="app-icon">C</div>
            <Text size="sm" fw={600}>
              Communications
            </Text>
          </Group>
          <NavLink component="a" href={customersUrl} label="Customers" rightSection={<IconArrowUpRight size={14} />} />
        </AppShell.Section>
      </AppShell.Navbar>
      <AppShell.Main>
        <Outlet />
      </AppShell.Main>
    </AppShell>
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
