import { Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { AddSecretForm, CredentialList } from "../components/credentials";
import { PermissionsStep } from "../components/permissions-step";
import { RegisterForm, RegisterRead } from "../components/register-form";
import { OwnerOnly, StepCard } from "../components/step-card";
import "../i18n";
import { stepsDone } from "../lib/steps";
import { TabFrame } from "./tab-frame";

/**
 * The **Connection** tab: wizard steps 1 to 3 (research §7.5) — register the
 * application, add a client secret, grant the permissions and the role and
 * test the connection — each with the portal path, what to copy and its
 * *Done* mark. They are the Owner's: an `entra:manage` holder who is not one
 * reads them without their forms. The secret's rotation lives here for the
 * connection's life.
 */
export function EntraConnectionTab() {
  const { t } = useI18n("entra");
  return (
    <TabFrame>
      {({ connection, owner }) => {
        const done = stepsDone(connection);
        return (
          <>
            <StepCard step={1} title={t("step1Title")} path={t("step1Path")} done={done.register}>
              <Text size="sm">{t("step1Body")}</Text>
              {owner ? <RegisterForm connection={connection} /> : <RegisterRead connection={connection} />}
            </StepCard>

            <StepCard step={2} title={t("step2Title")} path={t("step2Path")} done={done.secret}>
              <Text size="sm">{t("step2Body")}</Text>
              <CredentialList credentials={connection.credentials} owner={owner} />
              {owner ? <AddSecretForm /> : <OwnerOnly />}
            </StepCard>

            <StepCard step={3} title={t("step3Title")} path={t("step3Path")} done={done.permissions}>
              <PermissionsStep connection={connection} owner={owner} />
            </StepCard>
          </>
        );
      }}
    </TabFrame>
  );
}
