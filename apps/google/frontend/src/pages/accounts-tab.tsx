import { Anchor, Stack, Text } from "@mantine/core";
import { appUrl, useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { AccountsCard } from "../components/accounts-card";
import { GroupsCard } from "../components/groups-card";
import { LicencesCard } from "../components/licences-card";
import { SettingsForm } from "../components/settings-form";
import { OwnerOnly, SectionCard } from "../components/step-card";
import "../i18n";
import { TabFrame } from "./tab-frame";

/** Google's issuer, the authority identity's Google provider must name. */
const googleAuthority = "https://accounts.google.com";

/**
 * What the Sign-in step hands the host's single sign-on form (Google Workspace
 * design G24): Google, the Internal web client and the organisation's verified
 * domains as the allowed domains, for an empty form only.
 */
export interface SignInDefaults {
  provider: "google";
  authority: string;
  clientId: string;
  allowedDomains: string[];
}

export interface GoogleAccountsTabProps {
  /** The Sign-in step: identity's OIDC form, which the host embeds pre-filled. */
  signIn: (defaults: SignInDefaults) => ReactNode;
  /** The Provisioning step: identity's SCIM token card, which the host embeds. */
  provisioning: () => ReactNode;
}

/**
 * The **Accounts** tab (Google Workspace design G17–G20, G24), `google:manage`'s
 * as the whole page is, not only the Owner's: the account rules; the Sign-in
 * and Provisioning steps — identity's forms, handed in by the host and the
 * Owner's — once a test has read the organisation; the organisation's
 * licences; and the users and groups the module made or found for Staff, with
 * the confirmation of one it found rather than made.
 */
export function GoogleAccountsTab({ signIn, provisioning }: GoogleAccountsTabProps) {
  const { t } = useI18n("google");
  return (
    <TabFrame>
      {({ connection, owner }) => (
        <>
          <SectionCard title={t("accountRules")}>
            <Text size="sm">{t("accountRulesBody")}</Text>
            <SettingsForm connection={connection} />
          </SectionCard>
          {connection.customerId && (
            <>
              <SectionCard title={t("signInTitle")}>
                <Text size="sm">{t("signInBody")}</Text>
                <Text size="sm">{t("signInConnect")}</Text>
                <Text size="sm" c="dimmed">
                  {t("signInWithout")}
                </Text>
                {!owner ? (
                  <OwnerOnly />
                ) : connection.oauthClientId ? (
                  signIn({
                    provider: "google",
                    authority: googleAuthority,
                    clientId: connection.oauthClientId,
                    allowedDomains: connection.domains,
                  })
                ) : (
                  <Text size="sm" c="orange">
                    {t("signInNeedsClient")}
                  </Text>
                )}
              </SectionCard>
              <SectionCard title={t("provisioningTitle")}>
                <Text size="sm">{t("provisioningBody")}</Text>
                {owner ? provisioning() : <OwnerOnly />}
                <Stack gap={4}>
                  <Text size="sm">{t("defaultKind")}</Text>
                  <Anchor size="sm" href={appUrl("/staff/settings")}>
                    {t("openStaffSettings")}
                  </Anchor>
                </Stack>
              </SectionCard>
            </>
          )}
          <LicencesCard connection={connection} />
          <AccountsCard connection={connection} />
          <GroupsCard />
        </>
      )}
    </TabFrame>
  );
}
