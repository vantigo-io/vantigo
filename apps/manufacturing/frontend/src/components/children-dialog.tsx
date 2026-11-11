import { Alert, Button, Checkbox, Group, Modal, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useState } from "react";
import { createChildOrders, type ManufacturingOrder } from "../api/orders";
import { isRevisionConflict } from "../api/request";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { subAssemblyLines } from "../lib/sub-assembly";

export interface ChildrenDialogProps {
  order: ManufacturingOrder;
  opened: boolean;
  onClose: () => void;
}

/**
 * Create sub-assembly orders (M47): every made component line missing
 * something, each checked, for its missing quantity. The orders are made
 * confirmed, due when the parent starts (M37). Never automatic.
 */
export const ChildrenDialog = ({ order, opened, onClose }: ChildrenDialogProps) => {
  const { t } = useManufacturingFormat();
  return (
    <Modal opened={opened} onClose={onClose} title={t("childrenTitle", { number: order.number })}>
      {opened && <ChildrenForm order={order} onClose={onClose} />}
    </Modal>
  );
};

const ChildrenForm = ({ order, onClose }: { order: ManufacturingOrder; onClose: () => void }) => {
  const { t, quantity } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const lines = subAssemblyLines(order);
  const [chosen, setChosen] = useState<number[]>(() => lines.map((line) => line.id as number));
  const [problem, setProblem] = useState<string>();
  const [saving, setSaving] = useState(false);

  const create = async () => {
    setSaving(true);
    setProblem(undefined);
    try {
      const answer = await createChildOrders(order.id, order.revision, chosen);
      notifications.show({
        color: "teal",
        title: t("childrenCreated", { count: answer.children.length }),
        message: answer.children.map((child) => child.number).join(", "),
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
      <Text size="sm">{t("childrenHint")}</Text>
      {lines.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("childrenNone")}
        </Text>
      ) : (
        <Stack gap="xs">
          {lines.map((line) => {
            const id = line.id as number;
            return (
              <Checkbox
                key={id}
                checked={chosen.includes(id)}
                onChange={(event) => {
                  const checked = event.currentTarget.checked;
                  setChosen((current) => (checked ? [...current, id] : current.filter((one) => one !== id)));
                }}
                label={t("childLine", {
                  sku: line.variant.sku,
                  name: line.variant.productName,
                  missing: quantity(line.missing, line.variant.unit),
                })}
              />
            );
          })}
        </Stack>
      )}
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotCreateChildren")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button loading={saving} disabled={chosen.length === 0} onClick={() => void create()}>
          {t("createChildren")}
        </Button>
      </Group>
    </Stack>
  );
};
