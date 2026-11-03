import { Text } from "@mantine/core";
import type { InventoryPackaging } from "../api/stock";
import { breakdown, breakdownText } from "../lib/breakdown";
import { useInventoryFormat } from "../lib/format";

export interface QuantityProps {
  value: number;
  unit: string;
  /** The variant's packagings: the quantity is broken down in them beneath it (I26). */
  packagings?: readonly InventoryPackaging[];
}

/**
 * A quantity in the variant's unit, and beneath it, when the variant has
 * packagings that fit, the same quantity in them, largest first: `1 830 pcs`
 * over `3 Euro pallet, 2 Box` (I26). The base quantity is always shown.
 */
export const Quantity = ({ value, unit, packagings }: QuantityProps) => {
  const { quantity, number } = useInventoryFormat();
  const parts = breakdown(value, packagings);
  return (
    <>
      {quantity(value, unit)}
      {parts && (
        <Text span size="xs" c="dimmed" display="block" data-breakdown>
          {breakdownText(parts, unit, number)}
        </Text>
      )}
    </>
  );
};
