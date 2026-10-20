import { Stack } from "@mantine/core";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import "../i18n";

/**
 * The module's one page: the connection's status and the wizard's six steps
 * (Entra design D15). For now it names itself; the status card and the steps
 * come with the app (plan task 5).
 */
export function EntraPage() {
  const { t } = useI18n("entra");
  return (
    <Stack gap="lg">
      <PageHeader title={t("entra")} description={t("entraDescription")} />
    </Stack>
  );
}
