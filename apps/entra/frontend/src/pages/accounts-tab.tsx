import { Anchor, Stack, Text } from "@mantine/core";
import { appUrl, useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { GroupsCard } from "../components/groups-card";
import { SettingsForm } from "../components/settings-form";
import { OwnerOnly, StepCard } from "../components/step-card";
import "../i18n";
import { entraAuthority, stepsDone } from "../lib/steps";
import { TabFrame } from "./tab-frame";

/** What step 5 hands the host's sign-in form: the tenant's authority and the client, for an empty form only. */
export interface SignInDefaults {
  provider: "entra";
  authority: string;
  clientId: string;
}

export interface EntraAccountsTabProps {
  /** Step 5: identity's OIDC form, which the host embeds pre-filled (Entra design D15). */
  signIn: (defaults: SignInDefaults) => ReactNode;
  /** Step 6: identity's SCIM token card, which the host embeds. */
  provisioning: () => ReactNode;
}

/**
 * The **Accounts** tab: step 4, the account rules (`entra:manage`'s, not only
 * the Owner's); steps 5 and 6, identity's sign-in and provisioning forms,
 * handed in by the host and the Owner's; and the **Groups** table once the
 * connection is more than unconfigured.
 */
export function EntraAccountsTab({ signIn, provisioning }: EntraAccountsTabProps) {
  const { t } = useI18n("entra");
  return (
    <TabFrame>
      {({ connection, owner }) => {
        const done = stepsDone(connection);
        const { tenantId, clientId } = connection;
        return (
          <>
            <StepCard step={4} title={t("step4Title")} path={t("step4Path")} done={done.accounts}>
              <Text size="sm">{t("step4Body")}</Text>
              <SettingsForm connection={connection} />
            </StepCard>

            <StepCard step={5} title={t("step5Title")} path={t("step5Path")} done={undefined}>
              <Text size="sm">{t("step5Body")}</Text>
              <Text size="sm" c="dimmed">
                {t("step5Without")}
              </Text>
              {!owner ? (
                <OwnerOnly />
              ) : tenantId && clientId ? (
                signIn({ provider: "entra", authority: entraAuthority(tenantId), clientId })
              ) : (
                <Text size="sm" c="orange">
                  {t("step5NeedsRegister")}
                </Text>
              )}
            </StepCard>

            <StepCard step={6} title={t("step6Title")} path={t("step6Path")} done={undefined}>
              <Text size="sm">{t("step6Body")}</Text>
              <Text size="sm">{t("step6Extension")}</Text>
              <Text size="sm" c="dimmed">
                {t("step6Without")}
              </Text>
              {owner ? provisioning() : <OwnerOnly />}
              <Stack gap={4}>
                <Text size="sm">{t("defaultKind")}</Text>
                <Anchor size="sm" href={appUrl("/staff/settings")}>
                  {t("openStaffSettings")}
                </Anchor>
              </Stack>
            </StepCard>

            {connection.status !== "unconfigured" && <GroupsCard />}
          </>
        );
      }}
    </TabFrame>
  );
}
