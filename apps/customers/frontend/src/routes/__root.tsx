import {
  AppShell,
  Avatar,
  Burger,
  Divider,
  Group,
  Image,
  Kbd,
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
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { createRootRouteWithContext, Link, Outlet, useRouterState } from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";

import logo from "../assets/logo.png";
import { AppSpotlight } from "../components/app-spotlight";
import { mockUser } from "../lib/mock-user";

const navItems = [
  { label: "Dashboard", to: "/", icon: IconLayoutDashboard },
  { label: "Customers", to: "/customers", icon: IconUsers },
  { label: "Contacts", to: "/contacts", icon: IconAddressBook },
] as const;

const RootLayout = () => {
  const [opened, { toggle, close }] = useDisclosure();
  const pathname = useRouterState({ select: (state) => state.location.pathname });

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
                    {mockUser.initials}
                  </Avatar>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <Text size="sm" fw={500} truncate>
                      {mockUser.name}
                    </Text>
                    <Text size="xs" c="dimmed" truncate>
                      {mockUser.email}
                    </Text>
                  </div>
                  <IconChevronRight size={14} stroke={1.5} />
                </Group>
              </UnstyledButton>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item leftSection={<IconSettings size={14} />}>Settings</Menu.Item>
              <Menu.Divider />
              <Menu.Item color="red" leftSection={<IconLogout size={14} />}>
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
  component: RootLayout,
});
