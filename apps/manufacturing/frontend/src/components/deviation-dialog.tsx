import { Alert, Button, Checkbox, Group, Modal, Stack, Textarea, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { createDeviation, type ManufacturingDeviation } from "../api/quality";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";

export interface DeviationDialogProps {
  opened: boolean;
  onClose: () => void;
  /** What it is reported from: an order, a work order of it, or a lot. */
  orderId?: number;
  workOrderId?: number;
  lotId?: number;
  onCreated?: (deviation: ManufacturingDeviation) => void;
}

/**
 * Report a deviation (manufacturing phase 2 M99; nb Meld avvik): a title, a
 * description and, ticked, Put the output in quarantine — every lot the
 * order made so far, and those it makes while the deviation is open, and the
 * lot it is reported from, held until it is closed.
 */
export const DeviationDialog = ({ opened, onClose, orderId, workOrderId, lotId, onCreated }: DeviationDialogProps) => {
  const { t } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [quarantine, setQuarantine] = useState(false);
  const report = useMutation({
    mutationFn: () =>
      createDeviation({
        title: title.trim(),
        ...(description.trim() === "" ? {} : { description: description.trim() }),
        ...(orderId === undefined ? {} : { orderId }),
        ...(workOrderId === undefined ? {} : { workOrderId }),
        ...(lotId === undefined ? {} : { lotId }),
        quarantine,
      }),
    onSuccess: async (deviation) => {
      notifications.show({ color: "teal", title: t("deviationReported"), message: deviation.number });
      setTitle("");
      setDescription("");
      setQuarantine(false);
      await invalidate();
      onClose();
      onCreated?.(deviation);
    },
  });

  return (
    <Modal opened={opened} onClose={onClose} title={t("reportDeviation")} size="lg">
      <Stack gap="md" data-assist="manufacturing.deviation.report">
        <TextInput
          size="md"
          label={t("deviationTitle")}
          withAsterisk
          maxLength={200}
          value={title}
          onChange={(event) => setTitle(event.currentTarget.value)}
        />
        <Textarea
          size="md"
          label={t("deviationDescription")}
          autosize
          minRows={3}
          maxLength={5000}
          value={description}
          onChange={(event) => setDescription(event.currentTarget.value)}
        />
        <Checkbox
          size="md"
          label={t("putInQuarantine")}
          description={t("putInQuarantineHint")}
          checked={quarantine}
          onChange={(event) => setQuarantine(event.currentTarget.checked)}
        />
        {report.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusalMessage(t, report.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" size="md" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            size="md"
            color="red"
            loading={report.isPending}
            disabled={title.trim() === ""}
            onClick={() => report.mutate()}
          >
            {t("reportDeviation")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
