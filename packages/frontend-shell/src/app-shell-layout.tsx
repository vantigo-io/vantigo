import { Anchor, AppShell, Box, Burger, Divider, Group, Image, Text, Title } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import type { ReactNode } from "react";
import { appConfig, hasSupportContact } from "./app-config";
import { registerCatalog, useI18n } from "./i18n";
import { shellCatalog } from "./i18n/catalogs/shell";
import { type ShellLinkComponent, ShellLinkProvider } from "./link-context";
import { vantigoLogo } from "./logo";

registerCatalog("shell", shellCatalog);

export interface AppShellLayoutProps {
  /** Shown next to the logo (the active app's name). Defaults to the runtime app title. */
  title?: string;
  /** Header center slot, e.g. the spotlight search box. Hidden below the `sm` breakpoint. */
  headerCenter?: ReactNode;
  /** Header right slot: the mobile search trigger, the app switcher, the account menu. */
  headerActions?: ReactNode;
  /** The sidebar navigation. Omit to render no sidebar and no burger. Call `closeMobileNav` when a nav item is clicked. */
  nav?: (closeMobileNav: () => void) => ReactNode;
  /** Optional overlay rendered with access to the mobile-nav close callback. */
  overlay?: (closeMobileNav: () => void) => ReactNode;
  /** The host's router link, so shell components (breadcrumbs) navigate client-side. Plain anchors without it. */
  linkComponent?: ShellLinkComponent;
  children: ReactNode;
}

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

/** Where the logo leads: the "Home" app. */
const HOME_PATH = "/dashboard";

/** The logo's link home, client-side through the host's link component when it has one. */
const HomeLink = ({
  linkComponent: Link,
  label,
  children,
}: {
  linkComponent?: ShellLinkComponent;
  label: string;
  children: ReactNode;
}) =>
  Link ? (
    <Link to={HOME_PATH} aria-label={label}>
      {children}
    </Link>
  ) : (
    <a href={HOME_PATH} aria-label={label}>
      {children}
    </a>
  );

/**
 * The shared authenticated application shell: logo and app name on the left
 * of the header, search in the center, the app switcher and account menu on
 * the right; the active app's navigation in the sidebar (when it has one);
 * and — when support contact details are configured — a slim support footer.
 */
export const AppShellLayout = ({
  title,
  headerCenter,
  headerActions,
  nav,
  overlay,
  linkComponent,
  children,
}: AppShellLayoutProps) => {
  const [opened, { toggle, close }] = useDisclosure();
  const { t } = useI18n("shell");
  const config = appConfig();
  const showFooter = hasSupportContact(config);

  const shell = (
    <AppShell
      header={{ height: 60 }}
      navbar={nav ? { width: 260, breakpoint: "sm", collapsed: { mobile: !opened } } : undefined}
      footer={showFooter ? { height: 36 } : undefined}
      padding="md"
    >
      <AppShell.Header>
        <Group h="100%" px="md" gap="sm" wrap="nowrap">
          {nav && (
            <Burger
              opened={opened}
              onClick={toggle}
              hiddenFrom="sm"
              size="sm"
              aria-label={t(opened ? "closeNavigation" : "openNavigation")}
            />
          )}
          <HomeLink linkComponent={linkComponent} label={t("goToDashboard")}>
            <Image src={config.logoUrl ?? vantigoLogo} alt={config.title} h={32} w="auto" maw="30vw" fit="contain" />
          </HomeLink>
          <Divider orientation="vertical" my="md" />
          <Title
            order={4}
            fw={500}
            style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
          >
            {title ?? config.title}
          </Title>
          {headerCenter && (
            <Box flex={1} visibleFrom="sm" px="md">
              <Box maw={480} mx="auto">
                {headerCenter}
              </Box>
            </Box>
          )}
          <Group gap="xs" wrap="nowrap" ml="auto">
            {headerActions}
          </Group>
        </Group>
      </AppShell.Header>

      {nav && (
        <AppShell.Navbar
          p="md"
          component="nav"
          aria-label={t("primaryNavigation")}
          style={{ minHeight: 0, overflowY: "auto", overscrollBehavior: "contain" }}
        >
          <AppShell.Section grow style={{ flex: "1 0 auto" }}>
            {nav(close)}
          </AppShell.Section>
        </AppShell.Navbar>
      )}

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
  return linkComponent ? <ShellLinkProvider link={linkComponent}>{shell}</ShellLinkProvider> : shell;
};
