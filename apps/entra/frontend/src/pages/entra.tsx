import { Stack } from "@mantine/core";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import "../i18n";
import { EntraAccountsTab, type EntraAccountsTabProps } from "./accounts-tab";
import { EntraConnectionTab } from "./connection-tab";
import { EntraOutlookTab } from "./outlook-tab";
import { EntraStatusTab } from "./status-tab";

export type { SignInDefaults } from "./accounts-tab";

export type EntraPageProps = EntraAccountsTabProps;

/**
 * The module's page as one column: the four tabs one under the other.
 *
 * @deprecated The page is `/workspace/entra` in four tabs (Integrations design
 * T3); the host mounts `EntraStatusTab`, `EntraConnectionTab`, `EntraAccountsTab`
 * and `EntraOutlookTab` as routes. This composition stays exported for one
 * release so nothing else breaks, and is removed after it.
 */
export function EntraPage({ signIn, provisioning }: EntraPageProps) {
  const { t } = useI18n("entra");
  return (
    <Stack gap="lg">
      <PageHeader title={t("entra")} description={t("entraDescription")} />
      <EntraStatusTab />
      <EntraConnectionTab />
      <EntraAccountsTab signIn={signIn} provisioning={provisioning} />
      <EntraOutlookTab />
    </Stack>
  );
}
