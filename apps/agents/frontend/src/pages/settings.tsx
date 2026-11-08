import { Stack } from "@mantine/core";
import { PageHeader, PageSection, useI18n } from "@vantigo/frontend-shell";
import { SettingsForm } from "../components/settings-form";
import { type AgentsAccess, noAgentsAccess } from "../lib/access";
import "../i18n";

/** The company name and the retention (virtual employees design V11). */
export const SettingsPage = ({ access = noAgentsAccess }: { access?: AgentsAccess }) => {
  const { t } = useI18n("agents");
  return (
    <Stack gap="lg">
      <PageHeader title={t("settingsTitle")} description={t("settingsDescription")} />
      <PageSection>
        <SettingsForm canManage={access.canManage} />
      </PageSection>
    </Stack>
  );
};
