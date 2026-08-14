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
import { registerCatalog, useI18n } from "./i18n";
import { shellCatalog } from "./i18n/catalogs/shell";
import { vantigoLogo } from "./logo";

registerCatalog("shell", shellCatalog);

export interface ShellUser {
  displayName: string;
  email: string;
  avatarUrl?: string | null;
}

export interface AppShellLayoutProps {
  /** The module name displayed next to the logo. Defaults to the runtime app title. */
  moduleName?: string;
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
  /** Optional lower-pinned navigation, kept outside the primary scroll region. */
  navLower?: (closeMobileNav: () => void) => ReactNode;
  /** Optional overlay rendered with access to the mobile-nav close callback. */
  overlay?: (closeMobileNav: () => void) => ReactNode;
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
  const { t } = useI18n("shell");
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
        {t("helpCenter")}
      </Anchor>,
    );
  return (
    <Group gap="xs" wrap="nowrap">
      <Text size="xs" c="dimmed">
        {t("support")}
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
 * header, navigation in the sidebar, the user menu at the bottom of the
 * sidebar, and — when support contact details are configured — a slim support
 * footer.
 */
export const AppShellLayout = ({
  moduleName,
  user,
  userMenuItems,
  onSignOut,
  signOutDisabled,
  navbarTop,
  nav,
  navLower,
  overlay,
  children,
}: AppShellLayoutProps) => {
  const [opened, { toggle, close }] = useDisclosure();
  const { t } = useI18n("shell");
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
          <Burger
            opened={opened}
            onClick={toggle}
            hiddenFrom="sm"
            size="sm"
            aria-label={t(opened ? "closeNavigation" : "openNavigation")}
          />
          <Image src={config.logoUrl ?? vantigoLogo} alt={config.title} h={32} w="auto" maw="30vw" fit="contain" />
          <Divider orientation="vertical" my="md" />
          <Title
            order={4}
            fw={500}
            style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
          >
            {moduleName ?? config.title}
          </Title>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar
        p="md"
        component="nav"
        aria-label={t("primaryNavigation")}
        style={{ minHeight: 0, overflowY: "auto", overscrollBehavior: "contain" }}
      >
        {navbarTop && <AppShell.Section>{navbarTop}</AppShell.Section>}

        {/* The primary list grows into spare space, keeping the lower group and
            account controls at the bottom at normal heights. It does not
            shrink, so the navbar itself becomes the escape hatch on short or
            zoomed viewports instead of clipping those controls. */}
        <AppShell.Section grow mt="sm" style={{ flex: "1 0 auto" }}>
          {nav(close)}
        </AppShell.Section>

        {navLower && (
          <AppShell.Section mt="sm" style={{ flexShrink: 0 }}>
            <Divider mb="sm" />
            {navLower(close)}
          </AppShell.Section>
        )}

        <AppShell.Section style={{ flexShrink: 0 }}>
          <Divider mb="sm" />
          <Menu position="right-end" withArrow>
            <Menu.Target>
              <UnstyledButton
                w="100%"
                p="xs"
                aria-label={t("openAccountMenu")}
                aria-haspopup="menu"
                styles={{
                  root: {
                    "&:focus-visible": { outline: "2px solid var(--mantine-color-vantigo-5)", outlineOffset: 2 },
                  },
                }}
              >
                <Group gap="sm" wrap="nowrap">
                  <Avatar src={user?.avatarUrl} color="vantigo" radius="xl">
                    {user ? initials(user.displayName) : t("loadingIndicator")}
                  </Avatar>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <Text size="sm" fw={500} truncate>
                      {user?.displayName ?? t("loadingAccount")}
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
                {t("signOut")}
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main>{children}</AppShell.Main>

      {overlay?.(close)}

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
