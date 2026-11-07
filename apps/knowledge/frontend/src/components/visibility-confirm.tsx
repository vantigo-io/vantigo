import { Button, Group, Modal, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";

/**
 * The confirmation a move or an access change needs when it takes an article
 * between Private and the workspace (knowledge design K4): the server answered
 * 409 `visibility_change`, and the change is sent again with
 * `confirmVisibility` only once the person agrees.
 */
export const VisibilityConfirm = ({
  opened,
  onConfirm,
  onCancel,
  busy,
}: {
  opened: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  busy?: boolean;
}) => {
  const { t } = useI18n("knowledge");
  return (
    <Modal opened={opened} onClose={onCancel} title={t("visibilityTitle")} centered>
      <Text size="sm">{t("visibilityBody")}</Text>
      <Group justify="flex-end" mt="lg">
        <Button variant="default" onClick={onCancel}>
          {t("cancel")}
        </Button>
        <Button onClick={onConfirm} loading={busy}>
          {t("visibilityConfirm")}
        </Button>
      </Group>
    </Modal>
  );
};
