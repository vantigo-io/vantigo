"use client";

import { Button, Group, Modal, PasswordInput, Stack, Text } from "@mantine/core";
import { useTranslations } from "next-intl";
import { useState } from "react";

/**
 * Password-gated confirmation modal for sensitive account actions.
 * Resets its password field whenever it is closed.
 */
export function PasswordConfirmModal({
  opened,
  onClose,
  title,
  description,
  confirmLabel,
  confirmColor,
  isPending,
  onConfirm,
  children,
}: Readonly<{
  opened: boolean;
  onClose: () => void;
  title: string;
  description: string;
  confirmLabel: string;
  confirmColor?: string;
  isPending: boolean;
  onConfirm: (password: string) => Promise<boolean>;
  /** Optional result view; when set it replaces the password form. */
  children?: React.ReactNode;
}>) {
  const t = useTranslations("settings.twoFactor");
  const [password, setPassword] = useState("");

  const handleClose = () => {
    setPassword("");
    onClose();
  };

  const handleConfirm = async () => {
    if (await onConfirm(password)) {
      setPassword("");
    }
  };

  return (
    <Modal opened={opened} onClose={handleClose} title={title}>
      {children ?? (
        <Stack gap="md">
          <Text size="sm">{description}</Text>
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
            <Button
              color={confirmColor}
              loading={isPending}
              disabled={!password}
              onClick={handleConfirm}
            >
              {confirmLabel}
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
}
