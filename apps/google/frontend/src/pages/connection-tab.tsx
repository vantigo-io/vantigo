import { Anchor, Text } from "@mantine/core";
import { appUrl, useI18n } from "@vantigo/frontend-shell";
import { AdminForm, AdminRead } from "../components/admin-form";
import { KeyList, UploadKeyForm } from "../components/keys";
import { OAuthClientStep } from "../components/oauth-client";
import { CopyLine, OwnerOnly, StepCard } from "../components/step-card";
import { TestStep } from "../components/test-step";
import "../i18n";
import { stepsDone } from "../lib/steps";
import { TabFrame } from "./tab-frame";

/**
 * The **Connection** tab (Google Workspace design G13, G15, G16): the steps
 * in order — create the project, the service account and its key; upload the
 * key; delegate the scopes; name the admin to impersonate; test; and the
 * Internal web client. The writes are the Owner's: a `google:manage` holder
 * who is not one reads the steps without their forms.
 */
export function GoogleConnectionTab() {
  const { t, locale } = useI18n("google");
  return (
    <TabFrame>
      {({ connection, owner }) => {
        const done = stepsDone(connection);
        return (
          <>
            <StepCard step={1} title={t("step1Title")} path={t("step1Path")} done={undefined}>
              <Text size="sm">{t("step1Body")}</Text>
              <Text size="sm" c="orange">
                {t("step1OrgPolicy")}
              </Text>
              <Anchor
                size="sm"
                href={appUrl(`/docs/${locale}/admin/google-workspace/`)}
                target="_blank"
                rel="noopener noreferrer"
              >
                {t("guideLink")}
              </Anchor>
            </StepCard>

            <StepCard step={2} title={t("step2Title")} done={done.key}>
              <Text size="sm">{t("step2Body")}</Text>
              <KeyList credentials={connection.credentials} owner={owner} />
              {owner ? <UploadKeyForm /> : <OwnerOnly />}
            </StepCard>

            <StepCard step={3} title={t("step3Title")} path={t("step3Path")} done={undefined}>
              <Text size="sm">{t("step3Body")}</Text>
              {connection.delegationClientId ? (
                <CopyLine label={t("delegationClientId")} value={connection.delegationClientId} />
              ) : (
                <Text size="sm" c="dimmed">
                  {t("delegationClientIdPending")}
                </Text>
              )}
              <CopyLine
                label={t("scopesToDelegate")}
                value={connection.scopesToDelegate.join(",")}
                copyLabel={t("scopesToDelegateCopy")}
              />
              <Text size="sm" c="dimmed">
                {t("step3Wait")}
              </Text>
            </StepCard>

            <StepCard step={4} title={t("step4Title")} done={done.admin}>
              <Text size="sm">{t("step4Body")}</Text>
              {owner ? <AdminForm connection={connection} /> : <AdminRead connection={connection} />}
            </StepCard>

            <StepCard step={5} title={t("step5Title")} done={done.tested}>
              <Text size="sm">{t("step5Body")}</Text>
              <TestStep connection={connection} owner={owner} />
            </StepCard>

            <StepCard step={6} title={t("step6Title")} path={t("step6Path")} done={done.oauthClient}>
              <OAuthClientStep connection={connection} owner={owner} />
            </StepCard>
          </>
        );
      }}
    </TabFrame>
  );
}
