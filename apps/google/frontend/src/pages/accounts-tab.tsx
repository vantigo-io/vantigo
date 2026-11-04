import { Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { AccountsCard } from "../components/accounts-card";
import { GroupsCard } from "../components/groups-card";
import { LicencesCard } from "../components/licences-card";
import { SettingsForm } from "../components/settings-form";
import { SectionCard } from "../components/step-card";
import "../i18n";
import { TabFrame } from "./tab-frame";

/**
 * The **Accounts** tab (Google Workspace design G17–G20), `google:manage`'s
 * as the whole page is, not only the Owner's: the account rules, the
 * organisation's licences, and the users and groups the module made or found
 * for Staff — with the confirmation of one it found rather than made.
 */
export function GoogleAccountsTab() {
  const { t } = useI18n("google");
  return (
    <TabFrame>
      {({ connection }) => (
        <>
          <SectionCard title={t("accountRules")}>
            <Text size="sm">{t("accountRulesBody")}</Text>
            <SettingsForm connection={connection} />
          </SectionCard>
          <LicencesCard connection={connection} />
          <AccountsCard connection={connection} />
          <GroupsCard />
        </>
      )}
    </TabFrame>
  );
}
