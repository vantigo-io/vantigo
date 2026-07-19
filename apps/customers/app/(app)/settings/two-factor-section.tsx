"use client";

import { Badge, Button, Card, Group, Modal, Stack, Text, Title } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { BackupCodesList } from "@vantigo/customers/lib/components/backup-codes-list";
import { PasswordConfirmModal } from "@vantigo/customers/lib/components/password-confirm-modal";
import { TwoFactorEnrollment } from "@vantigo/customers/lib/components/two-factor-enrollment";
import { useAuthAction } from "@vantigo/customers/lib/settings/use-auth-action";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useState } from "react";

export function TwoFactorSection({
  enabled,
  enforced,
}: Readonly<{ enabled: boolean; enforced: boolean }>) {
  const t = useTranslations("settings.twoFactor");
  const router = useRouter();
  const { run, isPending } = useAuthAction();

  const [enrollOpened, enroll] = useDisclosure(false);
  const [disableOpened, disable] = useDisclosure(false);
  const [regenerateOpened, regenerate] = useDisclosure(false);
  const [newCodes, setNewCodes] = useState<string[]>([]);

  const handleDisable = async (password: string) => {
    const result = await run(() => authClient.twoFactor.disable({ password }), {
      success: t("disabled"),
      error: t("disableError"),
      onSuccess: disable.close,
    });
    return result !== null;
  };

  const handleRegenerate = async (password: string) => {
    const result = await run(() => authClient.twoFactor.generateBackupCodes({ password }), {
      error: t("regenerateError"),
      refresh: false,
    });
    if (!result?.data) return false;
    setNewCodes(result.data.backupCodes);
    return true;
  };

  const closeRegenerate = () => {
    setNewCodes([]);
    regenerate.close();
  };

  return (
    <Card withBorder maw={560}>
      <Stack gap="md">
        <Group gap="xs">
          <Title order={4}>{t("title")}</Title>
          <Badge color={enabled ? "teal" : "gray"} variant="light">
            {enabled ? t("statusEnabled") : t("statusDisabled")}
          </Badge>
        </Group>

        <Text size="sm" c="dimmed">
          {t("description")}
        </Text>

        {enabled ? (
          <Group>
            <Button variant="default" onClick={regenerate.open}>
              {t("regenerate")}
            </Button>
            {enforced ? (
              <Text size="sm" c="dimmed">
                {t("enforcedHint")}
              </Text>
            ) : (
              <Button variant="light" color="red" onClick={disable.open}>
                {t("disable")}
              </Button>
            )}
          </Group>
        ) : (
          <Group>
            <Button onClick={enroll.open}>{t("enable")}</Button>
          </Group>
        )}
      </Stack>

      <Modal opened={enrollOpened} onClose={enroll.close} title={t("enrollTitle")} size="lg">
        <TwoFactorEnrollment
          onEnabled={() => {
            enroll.close();
            router.refresh();
          }}
        />
      </Modal>

      <PasswordConfirmModal
        opened={disableOpened}
        onClose={disable.close}
        title={t("disableTitle")}
        description={t("disableDescription")}
        confirmLabel={t("disable")}
        confirmColor="red"
        isPending={isPending}
        onConfirm={handleDisable}
      />

      <PasswordConfirmModal
        opened={regenerateOpened}
        onClose={closeRegenerate}
        title={t("regenerateTitle")}
        description={t("regenerateDescription")}
        confirmLabel={t("regenerate")}
        isPending={isPending}
        onConfirm={handleRegenerate}
      >
        {newCodes.length > 0 ? (
          <Stack gap="md">
            <Text size="sm">{t("regenerateDone")}</Text>
            <BackupCodesList codes={newCodes} />
            <Group justify="flex-end">
              <Button onClick={closeRegenerate}>{t("close")}</Button>
            </Group>
          </Stack>
        ) : undefined}
      </PasswordConfirmModal>
    </Card>
  );
}
