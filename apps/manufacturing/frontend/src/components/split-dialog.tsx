import { ActionIcon, Alert, Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus, IconTrash } from "@tabler/icons-react";
import { useState } from "react";
import { type ManufacturingOrder, splitOrder } from "../api/orders";
import { isRevisionConflict } from "../api/request";
import { addDecimal, compareDecimal, isDecimal, isPositiveDecimal, normalizeDecimal } from "../lib/decimal";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";

/** A split's parts: at least two, at most twenty (manufacturing phase 2 M124). */
const MIN_PARTS = 2;
const MAX_PARTS = 20;

export interface SplitDialogProps {
  order: ManufacturingOrder;
  opened: boolean;
  onClose: () => void;
}

/**
 * Split (manufacturing phase 2 M124): the order's quantity in 2–20 parts that
 * sum to it; the order keeps the first, each other becomes a new order
 * numbered as a backorder. Every part starts unplanned, and a confirmed order
 * reserves again for each.
 */
export const SplitDialog = ({ order, opened, onClose }: SplitDialogProps) => {
  const { t } = useManufacturingFormat();
  return (
    <Modal opened={opened} onClose={onClose} title={t("splitOrderTitle", { number: order.number })}>
      {opened && <SplitForm order={order} onClose={onClose} />}
    </Modal>
  );
};

/** The parts' sum, or undefined while one is not a quantity more than 0. */
const partsSum = (parts: readonly string[]): string | undefined =>
  parts.every((part) => isDecimal(part) && isPositiveDecimal(part))
    ? parts.reduce((sum, part) => addDecimal(sum, normalizeDecimal(part)), "0")
    : undefined;

const SplitForm = ({ order, onClose }: { order: ManufacturingOrder; onClose: () => void }) => {
  const { t, quantity } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const [parts, setParts] = useState<string[]>(["", ""]);
  const [problem, setProblem] = useState<string>();
  const [saving, setSaving] = useState(false);
  const sum = partsSum(parts);
  const balanced = sum !== undefined && compareDecimal(sum, order.quantity) === 0;
  const unit = order.variant.unit;

  const split = async () => {
    setSaving(true);
    setProblem(undefined);
    try {
      const result = await splitOrder(order.id, order.revision, parts.map(normalizeDecimal));
      notifications.show({
        color: result.shortLines.length > 0 ? "yellow" : "teal",
        title: t("orderSplit"),
        message: [order.number, ...result.parts.map((part) => part.number)].join(", "),
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
      setSaving(false);
    }
  };

  return (
    <Stack gap="md">
      <Text size="sm">{t("splitOrderBody", { quantity: quantity(order.quantity, unit) })}</Text>
      {parts.map((part, index) => (
        // The parts are positional: the first is the order's own.
        <Group key={index} gap="xs" align="flex-end">
          <TextInput
            style={{ flex: 1 }}
            label={index === 0 ? t("splitKeeps", { number: order.number }) : t("splitPart", { number: index + 1 })}
            inputMode="decimal"
            value={part}
            rightSection={
              <Text size="xs" c="dimmed">
                {unit}
              </Text>
            }
            onChange={(event) => {
              const value = event.currentTarget.value;
              setParts((current) => current.map((one, at) => (at === index ? value : one)));
            }}
            error={
              part.trim() !== "" && (!isDecimal(part) || !isPositiveDecimal(part)) ? t("problem_quantity") : undefined
            }
          />
          {parts.length > MIN_PARTS && (
            <ActionIcon
              variant="subtle"
              color="red"
              mb={4}
              aria-label={t("removeSplitPart", { number: index + 1 })}
              onClick={() => setParts((current) => current.filter((_, at) => at !== index))}
            >
              <IconTrash size={16} />
            </ActionIcon>
          )}
        </Group>
      ))}
      <Group justify="space-between">
        <Button
          variant="subtle"
          size="xs"
          leftSection={<IconPlus size={14} />}
          disabled={parts.length >= MAX_PARTS}
          onClick={() => setParts((current) => [...current, ""])}
        >
          {t("addSplitPart")}
        </Button>
        <Text size="sm" c={sum !== undefined && !balanced ? "red" : "dimmed"}>
          {t("splitSum", { sum: quantity(sum ?? "0", unit), quantity: quantity(order.quantity, unit) })}
        </Text>
      </Group>
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSplit")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button loading={saving} disabled={!balanced} onClick={() => void split()}>
          {t("splitOrder")}
        </Button>
      </Group>
    </Stack>
  );
};
