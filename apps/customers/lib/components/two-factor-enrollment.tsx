"use client";

import {
  Alert,
  Button,
  Center,
  Checkbox,
  Code,
  Group,
  Image,
  PasswordInput,
  Stack,
  Text,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconShieldLock } from "@tabler/icons-react";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { BackupCodesList } from "@vantigo/customers/lib/components/backup-codes-list";
import { TotpPinInput } from "@vantigo/customers/lib/components/totp-pin-input";
import { totpSecretFromUri } from "@vantigo/customers/lib/settings/two-factor";
import { useTranslations } from "next-intl";
import { useState } from "react";

type Step = "password" | "save" | "verify";

/**
 * Shared TOTP enrollment flow: password -> QR + backup codes -> verify.
 * Used from the settings security tab and the enforced /two-factor/setup page.
 */
export function TwoFactorEnrollment({ onEnabled }: Readonly<{ onEnabled: () => void }>) {
  const t = useTranslations("twoFactor.enroll");
  const [step, setStep] = useState<Step>("password");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [totpUri, setTotpUri] = useState("");
  const [qrDataUrl, setQrDataUrl] = useState("");
  const [backupCodes, setBackupCodes] = useState<string[]>([]);
  const [savedCodes, setSavedCodes] = useState(false);
  const [code, setCode] = useState("");
  const [codeError, setCodeError] = useState<string | null>(null);

  const passwordForm = useForm({
    mode: "uncontrolled",
    initialValues: { password: "" },
    validate: {
      password: (value) => (value.length > 0 ? null : t("passwordRequired")),
    },
  });

  const handleEnable = async ({ password }: { password: string }) => {
    setIsSubmitting(true);
    const { data, error } = await authClient.twoFactor.enable({ password });

    if (error || !data) {
      setIsSubmitting(false);
      notifications.show({ color: "red", message: error?.message ?? t("enableError") });
      return;
    }

    const { toDataURL } = await import("qrcode");
    setQrDataUrl(await toDataURL(data.totpURI, { width: 220, margin: 1 }));
    setTotpUri(data.totpURI);
    setBackupCodes(data.backupCodes);
    setIsSubmitting(false);
    setStep("save");
  };

  const handleVerify = async (value: string) => {
    setIsSubmitting(true);
    setCodeError(null);
    const { error } = await authClient.twoFactor.verifyTotp({ code: value });
    setIsSubmitting(false);

    if (error) {
      setCode("");
      setCodeError(error.message ?? t("verifyError"));
      return;
    }

    notifications.show({ color: "green", message: t("enabled") });
    onEnabled();
  };

  if (step === "password") {
    return (
      <form onSubmit={passwordForm.onSubmit(handleEnable)}>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            {t("passwordDescription")}
          </Text>
          <PasswordInput
            label={t("passwordLabel")}
            withAsterisk
            autoComplete="current-password"
            key={passwordForm.key("password")}
            {...passwordForm.getInputProps("password")}
          />
          <Group justify="flex-end">
            <Button type="submit" loading={isSubmitting}>
              {t("continue")}
            </Button>
          </Group>
        </Stack>
      </form>
    );
  }

  if (step === "save") {
    const secret = totpSecretFromUri(totpUri);

    return (
      <Stack gap="md">
        <Text size="sm">{t("scanDescription")}</Text>
        <Center>
          <Image src={qrDataUrl} alt={t("qrAlt")} w={220} h={220} />
        </Center>
        {secret && (
          <Text size="xs" c="dimmed" ta="center">
            {t("manualEntry")} <Code data-testid="totp-secret">{secret}</Code>
          </Text>
        )}

        <Alert color="yellow" icon={<IconShieldLock size={16} />} title={t("backupTitle")}>
          {t("backupDescription")}
        </Alert>

        <BackupCodesList codes={backupCodes} />

        <Checkbox
          label={t("savedConfirm")}
          checked={savedCodes}
          onChange={(event) => setSavedCodes(event.currentTarget.checked)}
        />

        <Group justify="flex-end">
          <Button disabled={!savedCodes} onClick={() => setStep("verify")}>
            {t("continue")}
          </Button>
        </Group>
      </Stack>
    );
  }

  return (
    <Stack gap="md">
      <Text size="sm">{t("verifyDescription")}</Text>
      <TotpPinInput
        testId="enroll-pin"
        value={code}
        onChange={setCode}
        onComplete={handleVerify}
        disabled={isSubmitting}
        error={codeError}
      />
      <Group justify="space-between">
        <Button variant="subtle" onClick={() => setStep("save")}>
          {t("back")}
        </Button>
        <Button
          loading={isSubmitting}
          disabled={code.length < 6}
          onClick={() => handleVerify(code)}
        >
          {t("activate")}
        </Button>
      </Group>
    </Stack>
  );
}
