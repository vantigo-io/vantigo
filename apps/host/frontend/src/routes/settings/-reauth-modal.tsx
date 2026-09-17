import { Button, Group, Modal, PasswordInput, Stack, Text, TextInput } from "@mantine/core";
import type { UseFormReturnType } from "@mantine/form";
import { useTranslation } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import "../../i18n";

export interface ReauthValues {
  password: string;
  code: string;
}

export interface ReauthModalProps {
  opened: boolean;
  onClose: () => void;
  title: string;
  description?: ReactNode;
  form: UseFormReturnType<ReauthValues>;
  /** Also ask for an authenticator code, for changes that need a second factor. */
  requireCode?: boolean;
  confirmLabel: string;
  /** `red` for destructive changes, matching the shared confirm modal. */
  confirmColor?: "red";
  loading?: boolean;
  onSubmit: (values: ReauthValues) => void;
}

/**
 * The confirmation for a sensitive account change: the same shape as the
 * shared confirm modal (title, explanation, Cancel and a confirm button on
 * the right), plus the current password and, when asked, an authenticator
 * code, since the backend re-authenticates these requests.
 */
export const ReauthModal = ({
  opened,
  onClose,
  title,
  description,
  form,
  requireCode = false,
  confirmLabel,
  confirmColor,
  loading = false,
  onSubmit,
}: ReauthModalProps) => {
  const { t } = useTranslation("settings");
  const { t: hostT } = useTranslation("host");
  return (
    <Modal opened={opened} onClose={onClose} title={title} centered>
      <form onSubmit={form.onSubmit(onSubmit)}>
        <Stack>
          {description && <Text size="sm">{description}</Text>}
          <PasswordInput label={t("currentPassword")} data-autofocus {...form.getInputProps("password")} />
          {requireCode && <TextInput label={t("authenticatorCode")} {...form.getInputProps("code")} />}
          <Group justify="flex-end" mt="sm">
            <Button variant="default" onClick={onClose}>
              {hostT("common.cancel")}
            </Button>
            <Button type="submit" color={confirmColor} loading={loading}>
              {confirmLabel}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
