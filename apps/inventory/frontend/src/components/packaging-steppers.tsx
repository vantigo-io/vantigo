import { NumberInput } from "@mantine/core";
import type { InventoryPackaging } from "../api/stock";
import { useInventoryFormat } from "../lib/format";

export interface PackagingSteppersProps {
  packagings: readonly InventoryPackaging[];
  /** The variant's unit, in which each packaging's content is said. */
  unit: string;
  /** How many of each, by packaging id. */
  counts: Readonly<Record<number, number>>;
  onChange: (counts: Record<number, number>) => void;
  /** Each stepper's accessible name, from the packaging's. */
  label: (packaging: InventoryPackaging) => string;
}

/**
 * A whole-number stepper per packaging of the variant (I25): how many Euro
 * pallets, how many boxes. Each shows what one holds in the variant's unit.
 */
export const PackagingSteppers = ({ packagings, unit, counts, onChange, label }: PackagingSteppersProps) => {
  const { t, quantity } = useInventoryFormat();
  return (
    <>
      {packagings.map((packaging) => (
        <NumberInput
          key={packaging.id}
          size="xs"
          aria-label={label(packaging)}
          description={t("packagingHolds", { name: packaging.name, quantity: quantity(packaging.quantity, unit) })}
          allowDecimal={false}
          allowNegative={false}
          min={0}
          value={counts[packaging.id] ?? ""}
          onChange={(value) => onChange({ ...counts, [packaging.id]: value === "" ? 0 : Math.max(0, Number(value)) })}
        />
      ))}
    </>
  );
};
