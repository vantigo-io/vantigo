import { Button, Group, Modal, Stack, Text } from "@mantine/core";
import type { ReactNode } from "react";
import { useOrdersFormat } from "../lib/format";

export interface ConfirmDialogProps {
  title: string;
  children: ReactNode;
  confirmLabel: string;
  danger?: boolean;
  loading?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}

/** A state change that is not undone, asked once more: cancel, delete, cancel remaining, unlock. */
export const ConfirmDialog = ({
  title,
  children,
  confirmLabel,
  danger,
  loading,
  onConfirm,
  onClose,
}: ConfirmDialogProps) => {
  const { t } = useOrdersFormat();
  return (
    <Modal opened onClose={onClose} title={title}>
      <Stack>
        <Text size="sm" component="div">
          {children}
        </Text>
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancelDialog")}
          </Button>
          <Button color={danger ? "red" : undefined} loading={loading} onClick={onConfirm} data-autofocus>
            {confirmLabel}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
