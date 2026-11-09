import { Button, Group, Modal, Radio, Stack, Table, Text, Textarea } from "@mantine/core";
import { useState } from "react";
import { closeReasons, type InventoryTransferCloseReason } from "../api/transfers";
import { useInventoryFormat } from "../lib/format";

/** What a line will be short of once this receive lands. */
export interface Difference {
  key: string;
  label: string;
  quantity: number;
  unit: string;
}

export interface CloseDifferenceDialogProps {
  opened: boolean;
  onClose: () => void;
  transferNumber: number;
  differences: readonly Difference[];
  onConfirm: (close: { reason: InventoryTransferCloseReason; note?: string }) => void;
  loading?: boolean;
}

/**
 * Closing the difference (I42): what each line will still be short of, one
 * reason for all of it — lost, damaged, never packed or other — and an
 * optional note. The rest is recorded as missing on the transfer and added to
 * no stock: it left the from site when it was sent.
 */
export const CloseDifferenceDialog = ({
  opened,
  onClose,
  transferNumber,
  differences,
  onConfirm,
  loading,
}: CloseDifferenceDialogProps) => {
  const { t, quantity } = useInventoryFormat();
  const [reason, setReason] = useState<InventoryTransferCloseReason | null>(null);
  const [note, setNote] = useState("");
  const [error, setError] = useState<string>();

  return (
    <Modal opened={opened} onClose={onClose} title={t("closeDifference")}>
      <Stack gap="md">
        {differences.length > 0 && (
          <Table verticalSpacing={4}>
            <Table.Tbody>
              {differences.map((one) => (
                <Table.Tr key={one.key}>
                  <Table.Td>{one.label}</Table.Td>
                  <Table.Td ta="right">
                    {t("notReceivedQuantity", { quantity: quantity(one.quantity, one.unit) })}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
        <Radio.Group
          label={t("closeReasonQuestion")}
          withAsterisk
          value={reason}
          onChange={(value) => {
            setReason(value as InventoryTransferCloseReason);
            setError(undefined);
          }}
          error={error}
        >
          <Stack gap="xs" mt="xs">
            {closeReasons.map((value) => (
              <Radio key={value} value={value} label={t(`closeReason.${value}`)} />
            ))}
          </Stack>
        </Radio.Group>
        <Textarea
          label={t("closeNote")}
          maxLength={500}
          minRows={2}
          value={note}
          onChange={(event) => setNote(event.currentTarget.value)}
        />
        <Text size="sm" c="dimmed">
          {t("closeDifferenceHint", { number: transferNumber })}
        </Text>
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("back")}
          </Button>
          <Button
            loading={loading}
            onClick={() => {
              if (!reason) {
                setError(t("required"));
                return;
              }
              onConfirm({ reason, ...(note.trim() ? { note: note.trim() } : {}) });
            }}
          >
            {t("closeTransfer", { number: transferNumber })}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
