import { Button, Group, Modal, NumberInput, Stack, Table, Text } from "@mantine/core";
import { useState } from "react";
import type { DeliveredLine, Line } from "../api/types";
import { useOrdersFormat } from "../lib/format";
import { remainingToDeliver } from "../lib/lines";

export interface DeliveredDialogProps {
  /** Register adds delivered quantities; undo takes them back (O32). */
  mode: "mark" | "undo";
  lines: readonly Line[];
  loading?: boolean;
  error?: string;
  onSubmit: (lines: DeliveredLine[]) => void;
  onClose: () => void;
}

/**
 * Register delivered, or undo it (O32): a quantity per product line,
 * defaulting to what is left to deliver — or, to undo, to what was
 * delivered. A line left at 0 is not changed. The server refuses beyond
 * ordered less cancelled (`over_delivery`) and below 0. A kit
 * (manufacturing phase 2 M145) delivered by delivery documents is delivered
 * as its child lines and is not offered; a kit marked by hand marks its
 * children with it, and they are not offered.
 */
export const DeliveredDialog = ({ mode, lines, loading, error, onSubmit, onClose }: DeliveredDialogProps) => {
  const { t, quantity } = useOrdersFormat();
  const kits = new Set(lines.flatMap((line) => (line.parentLineId == null ? [] : [line.parentLineId])));
  const byDocuments = new Set(lines.filter((line) => line.deliveryMethod === "delivery").map((line) => line.id));
  const products = lines.filter(
    (line) =>
      line.kind === "product" &&
      !(kits.has(line.id) && byDocuments.has(line.id)) &&
      !(line.parentLineId != null && !byDocuments.has(line.parentLineId)),
  );
  const [quantities, setQuantities] = useState<Record<number, number | "">>(() =>
    Object.fromEntries(
      products.map((line) => [line.id, mode === "mark" ? remainingToDeliver(line) : line.qtyDelivered]),
    ),
  );
  const chosen = products
    .map((line) => ({ lineId: line.id, quantity: Number(quantities[line.id] || 0) }))
    .filter((line) => line.quantity > 0);
  return (
    <Modal opened onClose={onClose} size="lg" title={mode === "mark" ? t("registerDelivered") : t("undoDelivered")}>
      <Stack>
        <Text size="sm" c="dimmed">
          {mode === "mark" ? t("registerDeliveredHint") : t("undoDeliveredHint")}
        </Text>
        <Table.ScrollContainer minWidth={480}>
          <Table verticalSpacing={4}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("lineColumn")}</Table.Th>
                <Table.Th ta="right">{t("ordered")}</Table.Th>
                <Table.Th ta="right">{t("delivered")}</Table.Th>
                <Table.Th ta="right">{mode === "mark" ? t("deliverNow") : t("takeBack")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {products.map((line) => {
                const max = mode === "mark" ? remainingToDeliver(line) : line.qtyDelivered;
                return (
                  <Table.Tr key={line.id}>
                    <Table.Td>
                      <Text size="sm">{line.name ?? line.description}</Text>
                      {line.sku && (
                        <Text size="xs" c="dimmed" ff="monospace">
                          {line.sku}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td ta="right">
                      {quantity(line.quantity ?? 0)} {line.unit}
                    </Table.Td>
                    <Table.Td ta="right">{quantity(line.qtyDelivered)}</Table.Td>
                    <Table.Td w={140}>
                      <NumberInput
                        size="xs"
                        aria-label={t("deliverQuantityFor", { line: line.name ?? line.description })}
                        min={0}
                        max={max}
                        decimalScale={3}
                        disabled={max <= 0}
                        value={quantities[line.id] ?? ""}
                        onChange={(value) =>
                          setQuantities((current) => ({ ...current, [line.id]: value === "" ? "" : Number(value) }))
                        }
                      />
                    </Table.Td>
                  </Table.Tr>
                );
              })}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        {error && (
          <Text size="sm" c="red" role="alert">
            {error}
          </Text>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancelDialog")}
          </Button>
          <Button loading={loading} disabled={chosen.length === 0} onClick={() => onSubmit(chosen)}>
            {mode === "mark" ? t("registerDelivered") : t("undoDelivered")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
