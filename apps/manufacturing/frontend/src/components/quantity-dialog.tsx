import { Alert, Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useState } from "react";
import { registerQuantity } from "../api/shop-floor";
import { isDecimal, isPositiveDecimal, normalizeDecimal } from "../lib/decimal";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";

export interface QuantityDialogProps {
  workOrderId: number;
  orderNumber: string;
  unit: string;
  opened: boolean;
  onClose: () => void;
}

/**
 * Register quantity (manufacturing phase 2 M116): good units passed on to the
 * next operations, which may start once the send-ahead quantity is through.
 * Nothing is posted to stock. One `clientRequestId` per opening makes a retry
 * the same registration.
 */
export const QuantityDialog = ({ opened, onClose, ...props }: QuantityDialogProps) => {
  const { t } = useManufacturingFormat();
  return (
    <Modal opened={opened} onClose={onClose} title={t("registerQuantityTitle", { number: props.orderNumber })}>
      {opened && <QuantityForm {...props} onClose={onClose} />}
    </Modal>
  );
};

const QuantityForm = ({ workOrderId, unit, onClose }: Omit<QuantityDialogProps, "opened" | "orderNumber">) => {
  const { t } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const [clientRequestId] = useState(() => crypto.randomUUID());
  const [quantity, setQuantity] = useState("");
  const [problem, setProblem] = useState<string>();
  const [sending, setSending] = useState(false);
  const valid = isDecimal(quantity) && isPositiveDecimal(quantity);

  const send = async () => {
    setSending(true);
    setProblem(undefined);
    try {
      await registerQuantity(workOrderId, { clientRequestId, quantity: normalizeDecimal(quantity) });
      notifications.show({ color: "teal", title: t("quantityRegistered"), message: "" });
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
      <Text size="md">{t("registerQuantityHint")}</Text>
      <TextInput
        size="lg"
        label={t("quantity")}
        inputMode="decimal"
        value={quantity}
        rightSection={
          <Text size="sm" c="dimmed">
            {unit}
          </Text>
        }
        onChange={(event) => setQuantity(event.currentTarget.value)}
        error={quantity.trim() !== "" && !valid ? t("problem_quantity") : undefined}
      />
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotDoThat")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button size="lg" variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button size="lg" loading={sending} disabled={!valid} onClick={() => void send()}>
          {t("registerQuantity")}
        </Button>
      </Group>
    </Stack>
  );
};
