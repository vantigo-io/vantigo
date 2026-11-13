import { Alert, Button, Group, List, Modal, Select, SimpleGrid, Stack, Text, Textarea, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import {
  createUnbuild,
  type ManufacturingOrder,
  type ManufacturingUnbuild,
  orderLotsQueryOptions,
} from "../api/orders";
import { compareDecimal, isDecimal, isPositiveDecimal, normalizeDecimal, subtractDecimal } from "../lib/decimal";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { LocationSelect } from "./location-select";

export interface UnbuildDialogProps {
  order: ManufacturingOrder;
  opened: boolean;
  onClose: () => void;
}

/**
 * Unbuild (manufacturing phase 2 M123): takes apart what a done order made.
 * For a product tracked in lots or serials, as the catalog has it, the person
 * chooses the lot, among those the order's productions made, with what is
 * left of it; the components go back into the lots those productions took.
 * It is taken from the order's output location and put at its component
 * location unless another is chosen. A lot on hold may be taken apart: what
 * comes back is quarantined. The order's cost is not recomputed.
 */
export const UnbuildDialog = ({ order, opened, onClose }: UnbuildDialogProps) => {
  const { t } = useManufacturingFormat();
  return (
    <Modal opened={opened} onClose={onClose} title={t("unbuildTitle", { number: order.number })} size="lg">
      {opened && <UnbuildForm order={order} onClose={onClose} />}
    </Modal>
  );
};

const UnbuildForm = ({ order, onClose }: { order: ManufacturingOrder; onClose: () => void }) => {
  const { t, quantity } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const lots = useQuery(orderLotsQueryOptions(order.id));
  const [clientRequestId] = useState(() => crypto.randomUUID());
  const [lotId, setLotId] = useState<string | null>(null);
  const [fromLocationId, setFromLocationId] = useState<number | null>(order.outputLocationId);
  const [toLocationId, setToLocationId] = useState<number | null>(order.componentLocationId);
  const [amount, setAmount] = useState("");
  const [note, setNote] = useState("");
  const [problem, setProblem] = useState<string>();
  const [saving, setSaving] = useState(false);
  const [done, setDone] = useState<ManufacturingUnbuild>();
  const unit = order.variant.unit;
  const outputs = lots.data?.outputs ?? [];
  const tracked = lots.data !== undefined && lots.data.tracking !== "none";
  const chosen = outputs.find((lot) => String(lot.lotId) === lotId);
  const left = tracked
    ? chosen
      ? subtractDecimal(chosen.quantity, chosen.unbuilt)
      : undefined
    : subtractDecimal(order.produced, order.unbuilt);
  const valid =
    isDecimal(amount) &&
    isPositiveDecimal(amount) &&
    left !== undefined &&
    compareDecimal(normalizeDecimal(amount), left) <= 0 &&
    lots.data !== undefined &&
    (!tracked || chosen !== undefined);

  const post = async () => {
    setSaving(true);
    setProblem(undefined);
    try {
      const unbuild = await createUnbuild({
        clientRequestId,
        orderId: order.id,
        quantity: normalizeDecimal(amount),
        ...(tracked && chosen ? { lotId: chosen.lotId } : {}),
        ...(fromLocationId !== null ? { fromLocationId } : {}),
        ...(toLocationId !== null ? { toLocationId } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
      });
      notifications.show({ color: "teal", title: t("unbuildPosted"), message: unbuild.number });
      setDone(unbuild);
      await invalidate();
    } catch (error) {
      setProblem(refusalMessage(t, error as Error));
    } finally {
      setSaving(false);
    }
  };

  if (done)
    return (
      <Stack gap="md">
        <Text size="sm">{t("unbuildRestored", { number: done.number })}</Text>
        <List size="sm">
          {done.lines.map((line) => (
            <List.Item key={`${line.variant.id}-${line.lotId ?? 0}`}>
              {`${line.variant.sku} · ${quantity(line.quantity, line.variant.unit)}${line.lotNumber ? ` · ${line.lotNumber}` : ""}`}
            </List.Item>
          ))}
        </List>
        <Group justify="flex-end">
          <Button onClick={onClose}>{t("close")}</Button>
        </Group>
      </Stack>
    );

  return (
    <Stack gap="md">
      <Text size="sm">{t("unbuildBody")}</Text>
      {tracked && (
        <Select
          label={t("unbuildLot")}
          description={t("unbuildLotHint")}
          data={outputs.map((lot) => ({
            value: String(lot.lotId),
            label: t("unbuildLotOption", {
              number: lot.lotNumber,
              left: quantity(subtractDecimal(lot.quantity, lot.unbuilt), unit),
            }),
            disabled: compareDecimal(lot.unbuilt, lot.quantity) >= 0,
          }))}
          value={lotId}
          onChange={setLotId}
        />
      )}
      <TextInput
        label={t("quantity")}
        description={left !== undefined ? t("unbuildLeft", { quantity: quantity(left, unit) }) : undefined}
        inputMode="decimal"
        value={amount}
        rightSection={
          <Text size="xs" c="dimmed">
            {unit}
          </Text>
        }
        onChange={(event) => setAmount(event.currentTarget.value)}
        error={
          amount.trim() !== "" && (!isDecimal(amount) || !isPositiveDecimal(amount))
            ? t("problem_quantity")
            : amount.trim() !== "" &&
                left !== undefined &&
                isDecimal(amount) &&
                compareDecimal(normalizeDecimal(amount), left) > 0
              ? t("refusal.unbuild_too_much")
              : undefined
        }
      />
      <SimpleGrid cols={{ base: 1, sm: 2 }}>
        <LocationSelect
          label={t("unbuildFrom")}
          siteId={order.siteId}
          value={fromLocationId}
          onChange={setFromLocationId}
        />
        <LocationSelect
          label={t("unbuildTo")}
          description={t("unbuildToHint")}
          placeholder={t("unbuildToDefault")}
          clearable
          siteId={order.siteId}
          value={toLocationId}
          onChange={setToLocationId}
        />
      </SimpleGrid>
      <Textarea
        label={t("note")}
        maxLength={2000}
        rows={2}
        value={note}
        onChange={(event) => setNote(event.currentTarget.value)}
      />
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotUnbuild")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button loading={saving} disabled={!valid} onClick={() => void post()}>
          {t("unbuild")}
        </Button>
      </Group>
    </Stack>
  );
};
