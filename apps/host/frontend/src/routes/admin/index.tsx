import { Stack, Text, Title } from "@mantine/core";
import { createFileRoute } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { MaintenanceControls } from "./-maintenance-controls";
import "../../i18n";

export const Route = createFileRoute("/admin/")({
  component: AdminOverview,
});

export function AdminOverview() {
  const { t } = useI18n("host");

  return (
    <Stack gap="xl">
      <div>
        <Text tt="uppercase" size="xs" fw={700} c="dimmed">
          {t("systemAdmin.controlPlane")}
        </Text>
        <Title order={1}>{t("systemAdmin.title")}</Title>
        <Text c="dimmed">{t("systemAdmin.description")}</Text>
      </div>
      <MaintenanceControls />
    </Stack>
  );
}
