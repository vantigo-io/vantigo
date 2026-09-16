import { Avatar, Box, Menu, Text, UnstyledButton } from "@mantine/core";
import { IconLogout } from "@tabler/icons-react";
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

export interface AccountMenuProps {
  /** The signed-in user; undefined while loading. */
  user: ShellUser | undefined;
  /** Sections to show, already filtered to what the user may see. Empty sections should not be passed. */
  sections: readonly AccountMenuSection[];
  onSignOut: () => void;
  signOutDisabled?: boolean;
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
 * administrative destinations grouped under headings, and sign out.
 */
export const AccountMenu = ({ user, sections, onSignOut, signOutDisabled }: AccountMenuProps) => {
  const { t } = useI18n("shell");
  return (
    <Menu position="bottom-end" withArrow width={260}>
      <Menu.Target>
        <UnstyledButton
          aria-label={t("openAccountMenu")}
          aria-haspopup="menu"
          styles={{
            root: {
              borderRadius: "50%",
              "&:focus-visible": { outline: "2px solid var(--mantine-primary-color-filled)", outlineOffset: 2 },
            },
          }}
        >
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
        <Menu.Item color="red" leftSection={<IconLogout size={14} />} onClick={onSignOut} disabled={signOutDisabled}>
          {t("signOut")}
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  );
};
