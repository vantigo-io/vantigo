import { Alert, Button, Group, Modal, Radio, Stack, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useState } from "react";
import { blockWorkCenter, downtimeReasons, type ManufacturingDowntimeReason } from "../api/shop-floor";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";

/** The longest note a block takes. */
const NOTE_MAX = 2000;

export interface BlockDialogProps {
  workCenter: { id: number; code: string };
  opened: boolean;
  onClose: () => void;
}

/**
 * Block a work center (M30): a reason and an optional note. The running
 * productive logs at the center become downtime with that reason, and nothing
 * there can be started until it is unblocked.
 */
export const BlockDialog = ({ workCenter, opened, onClose }: BlockDialogProps) => {
  const { t } = useManufacturingFormat();
  return (
    <Modal opened={opened} onClose={onClose} title={t("blockTitle", { code: workCenter.code })} size="lg">
      {opened && <BlockForm workCenterId={workCenter.id} onClose={onClose} />}
    </Modal>
  );
};

const BlockForm = ({ workCenterId, onClose }: { workCenterId: number; onClose: () => void }) => {
  const { t } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const [reason, setReason] = useState<ManufacturingDowntimeReason>();
  const [note, setNote] = useState("");
  const [problem, setProblem] = useState<string>();
  const [sending, setSending] = useState(false);

  const block = async (chosen: ManufacturingDowntimeReason) => {
    setSending(true);
    setProblem(undefined);
    try {
      await blockWorkCenter(workCenterId, note.trim() ? { reason: chosen, note: note.trim() } : { reason: chosen });
      notifications.show({ color: "orange", title: t("workCenterBlocked"), message: t(`reason_${chosen}`) });
      await invalidate();
      onClose();
    } catch (error) {
      setProblem(refusalMessage(t, error as Error));
    } finally {
      setSending(false);
    }
  };

  return (
    <Stack gap="md">
      <Radio.Group
        label={t("blockReason")}
        value={reason ?? null}
        onChange={(value) => setReason(value as ManufacturingDowntimeReason)}
      >
        <Stack gap="sm" mt="xs">
          {downtimeReasons.map((value) => (
            <Radio key={value} size="lg" value={value} label={t(`reason_${value}`)} />
          ))}
        </Stack>
      </Radio.Group>
      <Textarea
        label={t("note")}
        rows={3}
        maxLength={NOTE_MAX}
        value={note}
        onChange={(event) => setNote(event.currentTarget.value)}
      />
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotBlock")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button size="lg" variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button size="lg" color="red" loading={sending} disabled={!reason} onClick={() => reason && void block(reason)}>
          {t("block")}
        </Button>
      </Group>
    </Stack>
  );
};
