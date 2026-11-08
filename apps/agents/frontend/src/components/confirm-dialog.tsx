import { Button, Group, Modal, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";

export interface ConfirmDialogProps {
  opened: boolean;
  title: string;
  body: string;
  confirmLabel: string;
  pending?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}

/** A destructive action asks first: the name of what goes, what it does, and a button that says so. */
export const ConfirmDialog = ({
  opened,
  title,
  body,
  confirmLabel,
  pending,
  onConfirm,
  onClose,
}: ConfirmDialogProps) => {
  const { t } = useI18n("agents");
  return (
    <Modal opened={opened} onClose={onClose} title={title}>
      <Text size="sm">{body}</Text>
      <Group justify="flex-end" mt="lg">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button color="red" loading={pending} onClick={onConfirm}>
          {confirmLabel}
        </Button>
      </Group>
    </Modal>
  );
};
