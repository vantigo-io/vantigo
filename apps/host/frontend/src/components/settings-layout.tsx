import { NavLink, Paper, Select, Stack, Text, Title } from "@mantine/core";
import { Link, useRouterState } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import type { ComponentType, ReactNode } from "react";
import "../i18n";

export type SettingsSection = { label: string; to: string; icon?: ComponentType<{ size?: number }> };

export function SettingsLayout({
  title,
  description,
  sections,
  children,
}: {
  title: string;
  description?: string;
  sections: readonly SettingsSection[];
  children: ReactNode;
}) {
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const { t } = useI18n("host");
  // Sections are plain paths — /settings/* for the personal account tree and
  // /workspace/* for the Owner-gated workspace tree.
  const active = sections.find((item) => pathname === item.to || pathname.startsWith(`${item.to}/`));
  return (
    <Stack maw={1180} mx="auto" gap="xl">
      <div>
        <Title order={2}>{title}</Title>
        {description && (
          <Text c="dimmed" mt={4}>
            {description}
          </Text>
        )}
      </div>
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "minmax(180px, 230px) minmax(0, 1fr)",
          gap: "var(--mantine-spacing-xl)",
        }}
      >
        <Paper withBorder p="xs" visibleFrom="sm">
          <Stack gap={2}>
            {sections.map((item) => (
              <NavLink
                key={item.to}
                component={Link}
                to={item.to}
                label={item.label}
                leftSection={item.icon ? <item.icon size={18} /> : undefined}
                active={active?.to === item.to}
              />
            ))}
          </Stack>
        </Paper>
        <div>
          <Select
            hiddenFrom="sm"
            mb="md"
            label={t("systemAdmin.settingsSection")}
            value={active?.to}
            data={sections.map((item) => ({ value: item.to, label: item.label }))}
            onChange={(value) => {
              if (value) window.location.assign(value);
            }}
          />
          {children}
        </div>
      </div>
    </Stack>
  );
}
