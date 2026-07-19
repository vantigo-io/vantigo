"use client";

import { Anchor, Button, Center, Checkbox, PinInput, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useState } from "react";

export function TwoFactorVerifyForm() {
  const t = useTranslations("twoFactor.verify");
  const router = useRouter();

  const [useBackupCode, setUseBackupCode] = useState(false);
  const [trustDevice, setTrustDevice] = useState(false);
  const [code, setCode] = useState("");
  const [backupCode, setBackupCode] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSuccess = () => {
    notifications.show({ color: "green", message: t("success") });
    router.push("/");
    router.refresh();
  };

  const handleVerifyTotp = async (value: string) => {
    setIsSubmitting(true);
    setError(null);
    const { error: verifyError } = await authClient.twoFactor.verifyTotp({
      code: value,
      trustDevice,
    });
    setIsSubmitting(false);

    if (verifyError) {
      setCode("");
      setError(verifyError.message ?? t("invalidCode"));
      return;
    }
    handleSuccess();
  };

  const handleVerifyBackupCode = async () => {
    if (!backupCode.trim()) return;
    setIsSubmitting(true);
    setError(null);
    const { error: verifyError } = await authClient.twoFactor.verifyBackupCode({
      code: backupCode.trim(),
      trustDevice,
    });
    setIsSubmitting(false);

    if (verifyError) {
      setError(verifyError.message ?? t("invalidBackupCode"));
      return;
    }
    handleSuccess();
  };

  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed" ta="center">
        {useBackupCode ? t("backupDescription") : t("description")}
      </Text>

      {useBackupCode ? (
        <TextInput
          label={t("backupCodeLabel")}
          value={backupCode}
          onChange={(event) => setBackupCode(event.currentTarget.value)}
          error={error}
          autoFocus
        />
      ) : (
        <>
          <Center>
            <PinInput
              length={6}
              type="number"
              oneTimeCode
              autoFocus
              data-testid="verify-pin"
              value={code}
              onChange={setCode}
              onComplete={handleVerifyTotp}
              disabled={isSubmitting}
              error={error !== null}
            />
          </Center>
          {error && (
            <Text size="sm" c="red" ta="center">
              {error}
            </Text>
          )}
        </>
      )}

      <Checkbox
        label={t("trustDevice")}
        checked={trustDevice}
        onChange={(event) => setTrustDevice(event.currentTarget.checked)}
      />

      <Button
        fullWidth
        loading={isSubmitting}
        disabled={useBackupCode ? backupCode.trim().length === 0 : code.length < 6}
        onClick={() => (useBackupCode ? handleVerifyBackupCode() : handleVerifyTotp(code))}
      >
        {t("submit")}
      </Button>

      <Text ta="center" size="sm">
        <Anchor
          component="button"
          type="button"
          onClick={() => {
            setUseBackupCode((value) => !value);
            setError(null);
          }}
        >
          {useBackupCode ? t("useTotpInstead") : t("useBackupInstead")}
        </Anchor>
      </Text>

      <Text ta="center" size="sm">
        <Anchor href="/sign-in">{t("backToSignIn")}</Anchor>
      </Text>
    </Stack>
  );
}
