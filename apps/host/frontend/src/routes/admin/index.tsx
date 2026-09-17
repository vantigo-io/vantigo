import { Stack } from "@mantine/core";
import { createFileRoute } from "@tanstack/react-router";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { MaintenanceControls } from "./-maintenance-controls";
import "../../i18n";

export const Route = createFileRoute("/admin/")({
  component: AdminOverview,
});

export function AdminOverview() {
  const { t } = useI18n("host");

  return (
    <Stack gap="xl">
      <PageHeader title={t("systemAdmin.controlPlane")} description={t("systemAdmin.description")} />
      <MaintenanceControls />
    </Stack>
  );
}
