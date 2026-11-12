import { Alert, Button, Group, Modal, NumberInput, Radio, Stack, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { downloadBlob, type InventoryLabelLayout, labelLayouts, MAX_LABELS } from "../api/labels";
import { printLotLabels } from "../api/lots";
import { refusalMessage, useInventoryFormat } from "../lib/format";

export interface PrintLotLabelsDialogProps {
  opened: boolean;
  onClose: () => void;
  /** The lots to label, in the order they print; at most 1000. */
  lotIds: readonly number[];
  /** What the PDF is named after: a lot's number, or the list's name. */
  name: string;
}

/**
 * Lot labels as a PDF (manufacturing phase 2 M73): each lot's LOT token as a
 * barcode, which a scan reads back, its number, its item and its expiry, on
 * an Avery L7160 or L7163 sheet, as many copies of each as asked. A serial is
 * a lot of its own, so serials get a label each. The PDF downloads.
 */
export const PrintLotLabelsDialog = ({ opened, onClose, lotIds, name }: PrintLotLabelsDialogProps) => {
  const { t } = useInventoryFormat();
  const [layout, setLayout] = useState<InventoryLabelLayout>("l7160");
  const [copies, setCopies] = useState<number | "">(1);
  const most = Math.max(1, Math.floor(MAX_LABELS / Math.max(1, lotIds.length)));

  const printing = useMutation({
    mutationFn: async () => {
      const pdf = await printLotLabels(lotIds, layout, Number(copies || 1));
      downloadBlob(pdf, `lot-labels-${name}.pdf`);
    },
    onSuccess: onClose,
  });

  return (
    <Modal opened={opened} onClose={onClose} title={t("printLotLabels")}>
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {t("printLotLabelsHint", { count: lotIds.length })}
        </Text>
        <NumberInput
          label={t("labelCopies")}
          min={1}
          max={most}
          allowDecimal={false}
          allowNegative={false}
          value={copies}
          onChange={(value) => setCopies(value === "" ? "" : Math.min(most, Math.max(1, Math.trunc(Number(value)))))}
        />
        <Radio.Group
          label={t("labelLayout")}
          value={layout}
          onChange={(value) => setLayout(value as InventoryLabelLayout)}
        >
          <Stack gap="xs" mt="xs">
            {labelLayouts.map((value) => (
              <Radio key={value} value={value} label={t(`labelLayout_${value}`)} />
            ))}
          </Stack>
        </Radio.Group>
        {printing.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotPrint")}>
            {refusalMessage(t, printing.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button onClick={() => printing.mutate()} loading={printing.isPending} disabled={lotIds.length === 0}>
            {t("downloadLabels")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
