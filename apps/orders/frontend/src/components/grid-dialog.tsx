import { Alert, Button, Group, Modal, NumberInput, ScrollArea, Stack, Table, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { variantGridQueryOptions } from "../api/grid";
import type { GridVariant } from "../api/types";
import type { VariantOption } from "../api/variants";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { VariantPicker } from "./variant-picker";

/** A variant and the quantity typed for it, as the grid hands it to the lines. */
export interface GridPick {
  variantId: number;
  sku: string;
  name: string;
  unit: string;
  quantity: number;
}

export interface GridDialogProps {
  currency: string;
  onAdd: (picks: GridPick[]) => void;
  onClose: () => void;
}

const sameValues = (variant: GridVariant, wanted: Record<string, string>) =>
  Object.entries(wanted).every(([name, value]) => variant.values[name] === value);

/**
 * Add from grid (O34): a product is picked by one of its variants, and its
 * variants are laid out along the options they differ by — size down, colour
 * across — with a quantity per cell. A product with more options, or more
 * values than a grid holds, is offered as a list. Each quantity typed adds
 * that variant's line, or updates the line it already has.
 */
export const GridDialog = ({ currency, onAdd, onClose }: GridDialogProps) => {
  const { t, unitPrice } = useOrdersFormat();
  const [picked, setPicked] = useState<VariantOption | null>(null);
  const [quantities, setQuantities] = useState<Record<number, number | "">>({});
  const grid = useQuery({ ...variantGridQueryOptions(picked?.productId ?? 0, currency), enabled: picked !== null });
  const data = grid.data;

  const cell = (variant: GridVariant | undefined, label: string) =>
    variant ? (
      <NumberInput
        size="xs"
        w={90}
        min={0}
        decimalScale={3}
        hideControls
        aria-label={label}
        value={quantities[variant.variantId] ?? ""}
        onChange={(value) =>
          setQuantities((current) => ({ ...current, [variant.variantId]: value === "" ? "" : Number(value) }))
        }
      />
    ) : (
      <Text size="xs" c="dimmed" ta="center">
        —
      </Text>
    );

  const add = () => {
    if (!data) return;
    const all = data.mode === "grid" ? data.cells : data.variants;
    onAdd(
      all
        .map((variant) => ({
          variantId: variant.variantId,
          sku: variant.sku,
          name: data.productName,
          unit: variant.unit,
          quantity: Number(quantities[variant.variantId] || 0),
        }))
        .filter((pick) => pick.quantity > 0),
    );
  };

  const matrix = () => {
    if (!data) return null;
    const [rows, columns] = data.options;
    if (!rows) return null;
    const across = columns?.values ?? [""];
    return (
      <Table withTableBorder withColumnBorders data-testid="variant-grid">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{columns ? `${rows.name} \\ ${columns.name}` : rows.name}</Table.Th>
            {columns ? across.map((value) => <Table.Th key={value}>{value}</Table.Th>) : <Table.Th />}
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.values.map((down) => (
            <Table.Tr key={down}>
              <Table.Th>{down}</Table.Th>
              {across.map((value) => {
                const wanted = columns ? { [rows.name]: down, [columns.name]: value } : { [rows.name]: down };
                const variant = data.cells.find((c) => sameValues(c, wanted));
                return (
                  <Table.Td key={value}>
                    {cell(variant, t("gridCellQuantity", { values: Object.values(wanted).join(" / ") }))}
                  </Table.Td>
                );
              })}
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    );
  };

  const list = () =>
    data && (
      <Table data-testid="variant-list">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("gridSku")}</Table.Th>
            <Table.Th>{t("gridOptions")}</Table.Th>
            <Table.Th ta="right">{t("unitPriceExclVat")}</Table.Th>
            <Table.Th>{t("quantity")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {data.variants.map((variant) => (
            <Table.Tr key={variant.variantId}>
              <Table.Td ff="monospace">{variant.sku}</Table.Td>
              <Table.Td>
                {Object.entries(variant.values)
                  .map(([name, value]) => `${name}: ${value}`)
                  .join(", ") || "—"}
              </Table.Td>
              <Table.Td ta="right">{variant.price == null ? "—" : unitPrice(variant.price, data.currency)}</Table.Td>
              <Table.Td>{cell(variant, t("gridCellQuantity", { values: variant.sku }))}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    );

  return (
    <Modal opened onClose={onClose} title={t("addFromGrid")} size="xl">
      <Stack>
        <VariantPicker
          label={t("gridProduct")}
          currency={currency}
          value={picked ? { variantId: picked.variantId, sku: picked.sku, name: picked.name } : null}
          onChange={(variant) => {
            setPicked(variant);
            setQuantities({});
          }}
        />
        {grid.isError && <Alert color="red">{refusalMessage(grid.error, t)}</Alert>}
        {data && (
          <>
            <Text size="sm" c="dimmed">
              {data.mode === "grid" ? t("gridHint") : t("gridListHint")}
            </Text>
            <ScrollArea.Autosize mah={420}>{data.mode === "grid" ? matrix() : list()}</ScrollArea.Autosize>
          </>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancelDialog")}
          </Button>
          <Button onClick={add} disabled={!data}>
            {t("addLines")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
