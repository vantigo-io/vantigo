import { Text } from "@mantine/core";
import { useInventoryFormat } from "../lib/format";
import { roundQuantity } from "../lib/lines";

/** The packaging kinds the catalog names (D I24); any other is said as a packaging. */
const packagingKinds = ["inner", "case", "pallet", "other"];

export interface SuggestionNoteProps {
  /** What the rule found needed to reach the target (I56). */
  needed: number;
  /** Needed, rounded up to whole packagings; needed when there is none (I57). */
  suggested: number;
  /** The smallest packaging it was rounded up to, and how many of it. */
  packaging?: { kind: string; quantity: number; count: number } | null;
  /** The placement's maximum, when it has one: rounding past it is said. */
  maximum?: number | null;
}

/**
 * The one place the rounding sentence is written (I57, the owner 2026-11-08):
 * *21 needed, rounded up to 2 cases of 15 = 30*, the packaging named by its
 * kind, and *…, 5 above the maximum of 40* when the rounding passes the
 * maximum. Without a packaging, or when nothing was rounded, it says what is
 * needed alone. The low-stock list and a drafted transfer line both show it.
 */
export const SuggestionNote = ({ needed, suggested, packaging, maximum }: SuggestionNoteProps) => {
  const { t, number } = useInventoryFormat();
  // Compared in thousandths, as quantities are kept (I11).
  const rounded = !!packaging && Math.round(suggested * 1000) > Math.round(needed * 1000);
  const parts = [
    rounded && packaging
      ? t("suggestionRounded", {
          needed: number(needed),
          count: packaging.count,
          kind: t(`packagingKind_${packagingKinds.includes(packaging.kind) ? packaging.kind : "other"}`, {
            count: packaging.count,
          }),
          per: number(packaging.quantity),
          suggested: number(suggested),
        })
      : t("suggestionNeeded", { needed: number(needed) }),
  ];
  if (rounded && maximum !== null && maximum !== undefined)
    parts.push(
      t("suggestionAboveMaximum", { over: number(roundQuantity(suggested - needed)), maximum: number(maximum) }),
    );
  return (
    <Text size="xs" c="dimmed" data-suggestion>
      {parts.join(", ")}
    </Text>
  );
};
