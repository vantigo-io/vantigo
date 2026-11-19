import { Avatar, Box, Menu, Text, UnstyledButton, useComputedColorScheme, useMantineColorScheme } from "@mantine/core";
import { IconCheck, IconLogout, IconMoon, IconSun } from "@tabler/icons-react";
import { type ComponentType, Fragment } from "react";
import { registerCatalog, useI18n } from "./i18n";
import { shellCatalog } from "./i18n/catalogs/shell";

registerCatalog("shell", shellCatalog);

export interface ShellUser {
  displayName: string;
  email: string;
  avatarUrl?: string | null;
}

export interface AccountMenuItem {
  /** Already translated. */
  label: string;
  icon: ComponentType<{ size?: number | string; stroke?: number | string }>;
  onSelect: () => void;
}

export interface AccountMenuSection {
  /** Already translated section heading. */
  label: string;
  items: readonly AccountMenuItem[];
}

/** The brands a person may choose among, shown in the account menu only when there is more than one (brands design B12). */
export interface BrandSwitcher {
  candidates: readonly { id: string; name: string }[];
  /** The brand the person chose, or null for automatic: the assignment's. */
  chosenId: string | null;
  /** The person's earlier choice no longer applies. */
  choiceIgnored?: boolean;
  /** A brand's id, or null for automatic. */
  onChoose: (brandId: string | null) => void;
  disabled?: boolean;
}

export interface AccountMenuProps {
  /** The signed-in user; undefined while loading. */
  user: ShellUser | undefined;
  /** Sections to show, already filtered to what the user may see. Empty sections should not be passed. */
  sections: readonly AccountMenuSection[];
  onSignOut: () => void;
  signOutDisabled?: boolean;
  /**
   * Called with the scheme the person switched to from the menu, so the host can
   * save it to their account. The menu applies it at once itself. Omit to show no
   * switch.
   */
  onColorSchemeChange?: (scheme: "light" | "dark") => void;
  /** Lists the person's brands to choose among. Omit, or give one candidate or none, to show no switcher. */
  brandSwitcher?: BrandSwitcher;
}

const initials = (name: string) =>
  name
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();

/**
 * The avatar menu in the header: who is signed in, the personal and
 * administrative destinations grouped under headings, a light/dark switch when
 * the host wants one, and sign out.
 */
export const AccountMenu = ({
  user,
  sections,
  onSignOut,
  signOutDisabled,
  onColorSchemeChange,
  brandSwitcher,
}: AccountMenuProps) => {
  const { t } = useI18n("shell");
  const { setColorScheme } = useMantineColorScheme();
  const scheme = useComputedColorScheme("light");
  const next = scheme === "dark" ? "light" : "dark";
  return (
    <Menu position="bottom-end" withArrow width={260}>
      <Menu.Target>
        <UnstyledButton aria-label={t("openAccountMenu")} aria-haspopup="menu" style={{ borderRadius: "50%" }}>
          <Avatar src={user?.avatarUrl} color="vantigo" radius="xl">
            {user ? initials(user.displayName) : t("loadingIndicator")}
          </Avatar>
        </UnstyledButton>
      </Menu.Target>
      <Menu.Dropdown>
        <Box px="sm" py="xs">
          <Text size="sm" fw={500} truncate>
            {user?.displayName ?? t("loadingAccount")}
          </Text>
          <Text size="xs" c="dimmed" truncate>
            {user?.email ?? ""}
          </Text>
        </Box>
        {sections.map((section) => (
          <Fragment key={section.label}>
            <Menu.Divider />
            <Menu.Label>{section.label}</Menu.Label>
            {section.items.map((item) => (
              <Menu.Item key={item.label} leftSection={<item.icon size={14} />} onClick={item.onSelect}>
                {item.label}
              </Menu.Item>
            ))}
          </Fragment>
        ))}
        <Menu.Divider />
        {brandSwitcher && brandSwitcher.candidates.length > 1 && (
          <>
            <Menu.Label>{t("brandSwitcher")}</Menu.Label>
            <Menu.Item
              closeMenuOnClick={false}
              disabled={brandSwitcher.disabled}
              rightSection={brandSwitcher.chosenId === null ? <IconCheck size={14} /> : null}
              onClick={() => brandSwitcher.onChoose(null)}
            >
              {t("brandAutomatic")}
            </Menu.Item>
            {brandSwitcher.candidates.map((candidate) => (
              <Menu.Item
                key={candidate.id}
                closeMenuOnClick={false}
                disabled={brandSwitcher.disabled}
                rightSection={brandSwitcher.chosenId === candidate.id ? <IconCheck size={14} /> : null}
                onClick={() => brandSwitcher.onChoose(candidate.id)}
              >
                {candidate.name}
              </Menu.Item>
            ))}
            {brandSwitcher.choiceIgnored && (
              <Text size="xs" c="dimmed" px="sm" pb="xs">
                {t("brandChoiceIgnored")}
              </Text>
            )}
            <Menu.Divider />
          </>
        )}
        {onColorSchemeChange && (
          <Menu.Item
            leftSection={next === "dark" ? <IconMoon size={14} /> : <IconSun size={14} />}
            onClick={() => {
              setColorScheme(next);
              onColorSchemeChange(next);
            }}
          >
            {t(next === "dark" ? "switchToDark" : "switchToLight")}
          </Menu.Item>
        )}
        <Menu.Item color="red" leftSection={<IconLogout size={14} />} onClick={onSignOut} disabled={signOutDisabled}>
          {t("signOut")}
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  );
};
