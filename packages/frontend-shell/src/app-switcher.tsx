import { ActionIcon, Popover, SimpleGrid, Text, ThemeIcon, Tooltip, UnstyledButton } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { IconGridDots } from "@tabler/icons-react";
import type { ComponentType } from "react";
import { registerCatalog, useI18n } from "./i18n";
import { shellCatalog } from "./i18n/catalogs/shell";

registerCatalog("shell", shellCatalog);

export interface SwitcherApp {
  /** Stable identifier, e.g. "customers". */
  id: string;
  /** Display name shown in the tile, already translated. */
  label: string;
  icon: ComponentType<{ size?: number | string; stroke?: number | string }>;
  /** Client-side navigation to the app's home. */
  onSelect: () => void;
  /** The app the user is in; rendered selected and inert. */
  current?: boolean;
  /** Present when the app is installed but turned off; shown muted as a caption, not selectable. */
  disabledReason?: string;
}

/**
 * The Google Workspace style application switcher in the header: a waffle
 * icon opening a grid of app tiles. Purely presentational — the host decides
 * which apps appear, which is current and which are disabled.
 */
export const AppSwitcher = ({ apps }: { apps: readonly SwitcherApp[] }) => {
  const [opened, { toggle, close }] = useDisclosure();
  const { t } = useI18n("shell");
  if (apps.length === 0) return null;

  return (
    <Popover
      opened={opened}
      onChange={(nextOpened) => {
        if (!nextOpened) close();
      }}
      position="bottom-end"
      withArrow
      shadow="md"
      width={380}
    >
      <Popover.Target>
        <Tooltip label={t("appsMenu")} openDelay={500}>
          <ActionIcon variant="subtle" color="gray" size="lg" radius="xl" aria-label={t("switchApp")} onClick={toggle}>
            <IconGridDots size={20} stroke={1.5} />
          </ActionIcon>
        </Tooltip>
      </Popover.Target>
      <Popover.Dropdown p="sm" role="dialog" aria-label={t("appsMenu")}>
        <SimpleGrid cols={Math.min(apps.length, 3)} spacing="xs">
          {apps.map((app) => {
            const disabled = app.disabledReason !== undefined;
            const inert = disabled || app.current === true;
            return (
              <UnstyledButton
                key={app.id}
                component="button"
                type="button"
                disabled={disabled}
                aria-disabled={disabled || undefined}
                aria-current={app.current ? "true" : undefined}
                p="xs"
                onClick={
                  inert
                    ? undefined
                    : () => {
                        close();
                        app.onSelect();
                      }
                }
                style={{
                  display: "flex",
                  flexDirection: "column",
                  alignItems: "center",
                  gap: 6,
                  minWidth: 0,
                  borderRadius: "var(--mantine-radius-md)",
                  background: app.current ? "var(--mantine-primary-color-light)" : undefined,
                  cursor: inert ? "default" : "pointer",
                  opacity: disabled ? 0.5 : undefined,
                }}
              >
                <ThemeIcon
                  size={38}
                  radius="md"
                  variant={app.current ? "filled" : "light"}
                  color={disabled ? "gray" : undefined}
                >
                  <app.icon size={22} stroke={1.5} />
                </ThemeIcon>
                <Text
                  fz={11}
                  fw={app.current ? 600 : 400}
                  ta="center"
                  w="100%"
                  lineClamp={2}
                  style={{ overflowWrap: "anywhere" }}
                >
                  {app.label}
                </Text>
                {disabled && (
                  <Text fz={10} c="dimmed" ta="center" w="100%">
                    {app.disabledReason}
                  </Text>
                )}
              </UnstyledButton>
            );
          })}
        </SimpleGrid>
      </Popover.Dropdown>
    </Popover>
  );
};
