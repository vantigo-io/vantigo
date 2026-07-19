"use client";

import {
  Badge,
  Button,
  Card,
  Code,
  Group,
  Modal,
  PasswordInput,
  SimpleGrid,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { TwoFactorEnrollment } from "@vantigo/customers/lib/components/two-factor-enrollment";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useState } from "react";

export function TwoFactorSection({
  enabled,
  enforced,
}: Readonly<{ enabled: boolean; enforced: boolean }>) {
  const t = useTranslations("settings.twoFactor");
  const router = useRouter();

  const [enrollOpened, enroll] = useDisclosure(false);
  const [disableOpened, disable] = useDisclosure(false);
  const [regenerateOpened, regenerate] = useDisclosure(false);

  return (
    <Card withBorder maw={560}>
      <Stack gap="md">
        <Group justify="space-between">
          <Group gap="xs">
            <Title order={4}>{t("title")}</Title>
            <Badge color={enabled ? "teal" : "gray"} variant="light">
              {enabled ? t("statusEnabled") : t("statusDisabled")}
            </Badge>
          </Group>
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

      <DisableModal opened={disableOpened} onClose={disable.close} />
      <RegenerateModal opened={regenerateOpened} onClose={regenerate.close} />
    </Card>
  );
}

function DisableModal({ opened, onClose }: Readonly<{ opened: boolean; onClose: () => void }>) {
  const t = useTranslations("settings.twoFactor");
  const router = useRouter();
  const [password, setPassword] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);

  const handleDisable = async () => {
    setIsSubmitting(true);
    const { error } = await authClient.twoFactor.disable({ password });
    setIsSubmitting(false);

    if (error) {
      notifications.show({ color: "red", message: error.message ?? t("disableError") });
      return;
    }
    notifications.show({ color: "green", message: t("disabled") });
    setPassword("");
    onClose();
    router.refresh();
  };

  return (
    <Modal opened={opened} onClose={onClose} title={t("disableTitle")}>
      <Stack gap="md">
        <Text size="sm">{t("disableDescription")}</Text>
        <PasswordInput
          label={t("passwordLabel")}
          value={password}
          onChange={(event) => setPassword(event.currentTarget.value)}
          autoComplete="current-password"
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button color="red" loading={isSubmitting} disabled={!password} onClick={handleDisable}>
            {t("disable")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

function RegenerateModal({ opened, onClose }: Readonly<{ opened: boolean; onClose: () => void }>) {
  const t = useTranslations("settings.twoFactor");
  const [password, setPassword] = useState("");
  const [codes, setCodes] = useState<string[]>([]);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const handleClose = () => {
    setPassword("");
    setCodes([]);
    onClose();
  };

  const handleRegenerate = async () => {
    setIsSubmitting(true);
    const { data, error } = await authClient.twoFactor.generateBackupCodes({ password });
    setIsSubmitting(false);

    if (error || !data) {
      notifications.show({ color: "red", message: error?.message ?? t("regenerateError") });
      return;
    }
    setCodes(data.backupCodes);
  };

  return (
    <Modal opened={opened} onClose={handleClose} title={t("regenerateTitle")}>
      {codes.length > 0 ? (
        <Stack gap="md">
          <Text size="sm">{t("regenerateDone")}</Text>
          <SimpleGrid cols={2} spacing="xs">
            {codes.map((code) => (
              <Code key={code} ta="center">
                {code}
              </Code>
            ))}
          </SimpleGrid>
          <Group justify="flex-end">
            <Button onClick={handleClose}>{t("close")}</Button>
          </Group>
        </Stack>
      ) : (
        <Stack gap="md">
          <Text size="sm">{t("regenerateDescription")}</Text>
          <PasswordInput
            label={t("passwordLabel")}
            value={password}
            onChange={(event) => setPassword(event.currentTarget.value)}
            autoComplete="current-password"
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={handleClose}>
              {t("cancel")}
            </Button>
            <Button loading={isSubmitting} disabled={!password} onClick={handleRegenerate}>
              {t("regenerate")}
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
}
