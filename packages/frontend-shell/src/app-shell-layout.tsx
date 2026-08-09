import {
  Anchor,
  AppShell,
  Avatar,
  Burger,
  Divider,
  Group,
  Image,
  Menu,
  Text,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { IconChevronRight, IconLogout } from "@tabler/icons-react";
import type { ReactNode } from "react";
import { appConfig, hasSupportContact } from "./app-config";
import { AppSwitcher, type ShellApp } from "./app-switcher";
import { vantigoLogo } from "./logo";

export interface ShellUser {
  displayName: string;
  email: string;
}

export interface AppShellLayoutProps {
  /** The module name displayed next to the logo. Defaults to the runtime app title. */
  moduleName?: string;
  /** The apps shown in the top-right application switcher (including the current one). */
  apps: readonly ShellApp[];
  /** The signed-in user shown in the sidebar user menu. */
  user: ShellUser | undefined;
  /** Extra items rendered in the user menu above the sign-out entry. */
  userMenuItems?: ReactNode;
  onSignOut: () => void;
  signOutDisabled?: boolean;
  /** Optional slot at the top of the sidebar (e.g. the spotlight search box). */
  navbarTop?: ReactNode;
  /** The sidebar navigation. Call `closeMobileNav` when a nav item is clicked. */
  nav: (closeMobileNav: () => void) => ReactNode;
  children: ReactNode;
}

const initials = (name: string) =>
  name
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();

/** The support contact line shown in the shell footer and on auth pages. */
export const SupportContactLine = () => {
  const { support } = appConfig();
  if (!hasSupportContact()) return null;
  const items: ReactNode[] = [];
  if (support.email)
    items.push(
      <Anchor key="email" href={`mailto:${support.email}`} size="xs" c="dimmed">
        {support.email}
      </Anchor>,
    );
  if (support.phone)
    items.push(
      <Anchor key="phone" href={`tel:${support.phone.replace(/\s+/g, "")}`} size="xs" c="dimmed">
        {support.phone}
      </Anchor>,
    );
  if (support.url)
    items.push(
      <Anchor key="url" href={support.url} target="_blank" rel="noreferrer" size="xs" c="dimmed">
        Help center
      </Anchor>,
    );
  return (
    <Group gap="xs" wrap="nowrap">
      <Text size="xs" c="dimmed">
        Support:
      </Text>
      {items.flatMap((item, index) =>
        index > 0
          ? [
              <Text key={`sep-${index}`} size="xs" c="dimmed">
                ·
              </Text>,
              item,
            ]
          : [item],
      )}
    </Group>
  );
};

/**
 * The shared authenticated application shell: logo plus module name in the
 * header, the application switcher in the top right, navigation in the
 * sidebar, the user menu at the bottom of the sidebar, and — when support
 * contact details are configured — a slim support footer.
 */
export const AppShellLayout = ({
  moduleName,
  apps,
  user,
  userMenuItems,
  onSignOut,
  signOutDisabled,
  navbarTop,
  nav,
  children,
}: AppShellLayoutProps) => {
  const [opened, { toggle, close }] = useDisclosure();
  const config = appConfig();
  const showFooter = hasSupportContact(config);

  return (
    <AppShell
      header={{ height: 60 }}
      navbar={{ width: 260, breakpoint: "sm", collapsed: { mobile: !opened } }}
      footer={showFooter ? { height: 36 } : undefined}
      padding="md"
    >
      <AppShell.Header>
        <Group h="100%" px="md" gap="sm" wrap="nowrap">
          <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" />
          <Image src={config.logoUrl ?? vantigoLogo} alt={config.title} h={32} w="auto" fit="contain" />
          <Divider orientation="vertical" my="md" />
          <Title order={4} fw={500}>
            {moduleName ?? config.title}
          </Title>
          <Group ml="auto" gap="xs">
            <AppSwitcher apps={apps} />
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="md">
        {navbarTop && <AppShell.Section>{navbarTop}</AppShell.Section>}

        <AppShell.Section grow mt="sm">
          {nav(close)}
        </AppShell.Section>

        <AppShell.Section>
          <Divider mb="sm" />
          <Menu position="right-end" withArrow>
            <Menu.Target>
              <UnstyledButton w="100%" p="xs">
                <Group gap="sm" wrap="nowrap">
                  <Avatar color="vantigo" radius="xl">
                    {user ? initials(user.displayName) : "\u2026"}
                  </Avatar>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <Text size="sm" fw={500} truncate>
                      {user?.displayName ?? "Loading account\u2026"}
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
              {userMenuItems}
              <Menu.Divider />
              <Menu.Item
                color="red"
                leftSection={<IconLogout size={14} />}
                onClick={onSignOut}
                disabled={signOutDisabled}
              >
                Sign out
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main>{children}</AppShell.Main>

      {showFooter && (
        <AppShell.Footer>
          <Group h="100%" px="md" justify="center">
            <SupportContactLine />
          </Group>
        </AppShell.Footer>
      )}
    </AppShell>
  );
};
