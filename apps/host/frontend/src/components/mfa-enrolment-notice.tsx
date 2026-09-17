import { Alert, List, Stack, Text, Title } from "@mantine/core";
import { IconShieldLock } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";

/**
 * The explanation shown at the top of the security tab while the session
 * must enrol in MFA (see lib/mfa-enrolment-gate.ts): why the user is here,
 * the steps the authenticator form below asks for, in order, and what
 * happens once it is done. Written for an administrator who has just
 * created the installation's first account and has never seen this app.
 */
export const MfaEnrolmentNotice = () => {
  const { t } = useI18n("host");
  return (
    <Alert color="yellow" variant="light" icon={<IconShieldLock size={22} />} data-testid="mfa-enrolment-notice">
      <Stack gap="sm">
        <Title order={3} size="h4">
          {t("settings.mfaRequiredTitle")}
        </Title>
        <Text size="sm">{t("settings.mfaRequiredWhy")}</Text>
        <List type="ordered" size="sm" spacing="xs">
          <List.Item>{t("settings.mfaRequiredStep1")}</List.Item>
          <List.Item>{t("settings.mfaRequiredStep2")}</List.Item>
          <List.Item>{t("settings.mfaRequiredStep3")}</List.Item>
          <List.Item>{t("settings.mfaRequiredStep4")}</List.Item>
          <List.Item>{t("settings.mfaRequiredStep5")}</List.Item>
        </List>
        <Text size="sm">{t("settings.mfaRequiredAfter")}</Text>
      </Stack>
    </Alert>
  );
};
