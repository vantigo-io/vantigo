import { Alert, Button, Group, Modal, Radio, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useState } from "react";
import { closeOrder, type ManufacturingOrder } from "../api/orders";
import { isRevisionConflict } from "../api/request";
import { compareDecimal } from "../lib/decimal";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";

export interface CloseDialogProps {
  order: ManufacturingOrder;
  opened: boolean;
  onClose: () => void;
}

/**
 * Close (M23): releases what is still reserved and marks the order done.
 * When less than the quantity was produced it asks up front whether to create
 * a backorder for the rest, and sends the answer — so the server never has
 * to refuse with quantity_short.
 */
export const CloseDialog = ({ order, opened, onClose }: CloseDialogProps) => {
  const { t } = useManufacturingFormat();
  return (
    <Modal opened={opened} onClose={onClose} title={t("closeOrderTitle", { number: order.number })}>
      {opened && <CloseForm order={order} onClose={onClose} />}
    </Modal>
  );
};

const CloseForm = ({ order, onClose }: { order: ManufacturingOrder; onClose: () => void }) => {
  const { t, quantity } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const short = compareDecimal(order.produced, order.quantity) < 0;
  const [backorder, setBackorder] = useState<"yes" | "no" | null>(null);
  const [problem, setProblem] = useState<string>();
  const [closing, setClosing] = useState(false);

  const close = async () => {
    setClosing(true);
    setProblem(undefined);
    try {
      const result = await closeOrder(order.id, order.revision, short ? backorder === "yes" : undefined);
      notifications.show({
        color: "teal",
        title: t("orderClosed"),
        message: result.backorder ? t("backorderCreated", { number: result.backorder.number }) : result.order.number,
      });
      if (result.shortLines.length > 0)
        notifications.show({
          color: "yellow",
          title: t("backorderShort"),
          message: result.shortLines.map((line) => line.variant.sku).join(", "),
        });
      await invalidate();
      onClose();
    } catch (error) {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await invalidate();
        onClose();
        return;
      }
      setProblem(refusalMessage(t, error as Error));
    } finally {
      setClosing(false);
    }
  };

  const unit = order.variant.unit;
  return (
    <Stack gap="md">
      <Text size="sm">{t("closeOrderBody")}</Text>
      {short && (
        <Radio.Group
          label={t("createBackorderQuestion")}
          description={t("createBackorderHint", {
            produced: quantity(order.produced, unit),
            quantity: quantity(order.quantity, unit),
          })}
          value={backorder}
          onChange={(value) => setBackorder(value as "yes" | "no")}
        >
          <Stack gap="xs" mt="xs">
            <Radio value="yes" label={t("createBackorderYes")} />
            <Radio value="no" label={t("createBackorderNo")} />
          </Stack>
        </Radio.Group>
      )}
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotClose")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button loading={closing} disabled={short && backorder === null} onClick={() => void close()}>
          {t("closeOrder")}
        </Button>
      </Group>
    </Stack>
  );
};
