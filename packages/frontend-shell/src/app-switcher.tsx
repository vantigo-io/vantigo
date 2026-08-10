import { ActionIcon, Popover, SimpleGrid, Text, ThemeIcon, Tooltip, UnstyledButton } from "@mantine/core";
import { IconGridDots } from "@tabler/icons-react";
import type { ComponentType } from "react";

export interface ShellApp {
  /** Stable identifier, e.g. "customers". */
  id: string;
  /** Display name shown in the switcher, e.g. "Customers". */
  label: string;
  /** Absolute URL of the app. Omitted for the current app. */
  url?: string;
  /** Optional client-side navigation callback for a single-SPA deployment. */
  onClick?: () => void;
  /** Tabler icon rendered in the app tile. */
  icon: ComponentType<{ size?: number | string; stroke?: number | string }>;
  /** Marks the app the user is currently in; rendered selected and not clickable. */
  current?: boolean;
}

/**
 * The Google Workspace style application switcher shown in the top right of
 * the header. The current app is highlighted and inert; the other tiles link
 * to the enabled sibling apps of the deployment.
 */
export const AppSwitcher = ({ apps }: { apps: readonly ShellApp[] }) => {
  if (apps.length === 0) return null;

  return (
    <Popover position="bottom-end" withArrow shadow="md" width={330}>
      <Popover.Target>
        <Tooltip label="Vantigo apps" openDelay={500}>
          <ActionIcon variant="subtle" color="gray" size="lg" radius="xl" aria-label="Switch app">
            <IconGridDots size={20} stroke={1.5} />
          </ActionIcon>
        </Tooltip>
      </Popover.Target>
      <Popover.Dropdown p="sm">
        <SimpleGrid cols={Math.min(apps.length, 3)} spacing="xs">
          {apps.map((app) => (
            <UnstyledButton
              key={app.id}
              component={app.current ? undefined : app.onClick ? "button" : "a"}
              href={app.current ? undefined : app.url}
              aria-current={app.current ? "true" : undefined}
              p="xs"
              onClick={app.current ? undefined : app.onClick}
              style={{
                display: "flex",
                flexDirection: "column",
                alignItems: "center",
                gap: 6,
                minWidth: 0,
                borderRadius: "var(--mantine-radius-md)",
                background: app.current ? "var(--mantine-color-vantigo-0)" : undefined,
                cursor: app.current ? "default" : "pointer",
              }}
            >
              <ThemeIcon size={38} radius="md" variant={app.current ? "filled" : "light"}>
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
            </UnstyledButton>
          ))}
        </SimpleGrid>
      </Popover.Dropdown>
    </Popover>
  );
};
