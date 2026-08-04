import {
  AppShell,
  Avatar,
  Burger,
  Center,
  Divider,
  Group,
  Image,
  Kbd,
  Loader,
  Menu,
  NavLink,
  Text,
  TextInput,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { useDisclosure, useOs } from "@mantine/hooks";
import { spotlight } from "@mantine/spotlight";
import {
  IconAddressBook,
  IconChevronRight,
  IconLayoutDashboard,
  IconLogout,
  IconSearch,
  IconSettings,
  IconUsers,
} from "@tabler/icons-react";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { createRootRouteWithContext, Link, Outlet, redirect, useRouterState } from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import { useEffect } from "react";
import { fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signOut } from "../api/auth";
import logo from "../assets/logo.png";
import { AppSpotlight } from "../components/app-spotlight";
import { isPublicRoute } from "../lib/public-routes";

const navItems = [
  { label: "Dashboard", to: "/", icon: IconLayoutDashboard },
  { label: "Customers", to: "/customers", icon: IconUsers },
  { label: "Contacts", to: "/contacts", icon: IconAddressBook },
] as const;

const RootLayout = () => {
  const [opened, { toggle, close }] = useDisclosure();
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
      window.location.assign("/sign-in");
    },
  });
  const user = session?.user;
  const isOwner = user?.roles.includes("Owner") ?? false;

  useEffect(() => {
    if (!publicRoute && !isPending && !session) window.location.assign("/sign-in");
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
    <AppShell
      header={{ height: 60 }}
      navbar={{ width: 260, breakpoint: "sm", collapsed: { mobile: !opened } }}
      padding="md"
    >
      <AppShell.Header>
        <Group h="100%" px="md" gap="sm">
          <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" />
          <Image src={logo} alt="Vantigo" h={32} w="auto" fit="contain" />
          <Divider orientation="vertical" my="md" />
          <Title order={4} fw={500}>
            Customers
          </Title>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="md">
        <AppShell.Section>
          <SpotlightSearchBox />
        </AppShell.Section>

        <AppShell.Section grow mt="sm">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              component={Link}
              to={item.to}
              label={item.label}
              leftSection={<item.icon size={18} stroke={1.5} />}
              active={item.to === "/" ? pathname === "/" : pathname.startsWith(item.to)}
              onClick={close}
            />
          ))}
        </AppShell.Section>

        <AppShell.Section>
          <Divider mb="sm" />
          <Menu position="right-end" withArrow>
            <Menu.Target>
              <UnstyledButton w="100%" p="xs">
                <Group gap="sm" wrap="nowrap">
                  <Avatar color="blue" radius="xl">
                    {user
                      ? user.displayName
                          .split(/\s+/)
                          .map((part) => part[0])
                          .join("")
                          .slice(0, 2)
                          .toUpperCase()
                      : "…"}
                  </Avatar>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <Text size="sm" fw={500} truncate>
                      {user?.displayName ?? "Loading account…"}
                    </Text>
                    <Text size="xs" c="dimmed" truncate>
                      {user?.email ?? ""}
                    </Text>
                  </div>
                  <IconChevronRight size={14} stroke={1.5} />
                </Group>
              </UnstyledButton>
            </Menu.Target>
            <Menu.Dropdown>
              {isOwner && (
                <Menu.Item component={Link} to="/settings" leftSection={<IconSettings size={14} />}>
                  Settings
                </Menu.Item>
              )}
              <Menu.Divider />
              <Menu.Item
                color="red"
                leftSection={<IconLogout size={14} />}
                onClick={() => logout.mutate()}
                disabled={logout.isPending}
              >
                Sign out
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main>
        <Outlet />
      </AppShell.Main>

      <AppSpotlight />
      <TanStackRouterDevtools />
      <ReactQueryDevtools />
    </AppShell>
  );
};

/**
 * A search-box-styled button opening the global spotlight, with the platform's
 * keyboard shortcut as a hint.
 */
const SpotlightSearchBox = () => {
  const os = useOs();
  const modKey = os === "macos" ? "\u2318" : "Ctrl";

  return (
    <TextInput
      component="button"
      type="button"
      onClick={spotlight.open}
      leftSection={<IconSearch size={16} stroke={1.5} />}
      rightSection={<Kbd size="xs">{modKey} + K</Kbd>}
      rightSectionWidth={70}
      aria-label="Search"
      styles={{ input: { cursor: "pointer" } }}
    >
      <Text size="sm" c="dimmed" component="span">
        Search
      </Text>
    </TextInput>
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
