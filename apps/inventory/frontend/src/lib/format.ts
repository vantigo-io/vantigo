import { useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { refusalCodes } from "../api/movements";
import { ApiValidationError, INVENTORY_QUERY_KEY, refusalCode } from "../api/request";
import "../i18n";

type TFunction = ReturnType<typeof useI18n>["t"];

/** At most three decimals, as quantities are stored (I11). */
export const QUANTITY_DECIMALS = 3;

/** The package's words and its ways of showing quantities and instants, in the viewer's language and zone. */
export const useInventoryFormat = () => {
  const { t, formatters } = useI18n("inventory");
  return {
    t,
    /** A quantity, with its unit when one is given: `1 830 pcs`. */
    quantity: (value: number, unit?: string) => {
      const number = formatters.formatNumber(value, { maximumFractionDigits: QUANTITY_DECIMALS });
      return unit ? `${number} ${unit}` : number;
    },
    /** An instant, day and time. */
    stamp: (instant: string) => formatters.formatDate(instant, { dateStyle: "medium", timeStyle: "short" }),
  };
};

/**
 * The words for a refusal: the module's own for a code it answers
 * (`refusal.<code>`), else what the server said — a field error's first
 * message, or the problem's detail. A failure with no HTTP status never
 * reached the server (a network error) and says so; an answer without words
 * of its own says its status.
 */
export const refusalMessage = (t: TFunction, error: Error): string => {
  const status = (error as { status?: unknown }).status;
  if (typeof status !== "number") return t("couldNotReach");
  const code = refusalCode(error);
  if (code && (refusalCodes as readonly string[]).includes(code)) return t(`refusal.${code}`);
  if (error instanceof ApiValidationError) {
    const first = Object.values(error.fieldErrors)[0];
    if (first) return first;
  }
  return error.message || t("requestFailed", { status });
};

/** Every write invalidates the whole package: a posting moves contents, product stock, movements and stats at once. */
export const useInvalidateInventory = () => {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: [INVENTORY_QUERY_KEY] });
};

/** How a variant reads in a picker and a row: its SKU, then its product. */
export const variantLabel = (variant: { sku: string; productName: string }) =>
  `${variant.sku} · ${variant.productName}`;

/** How a location reads in a list: its full code, then its name when it has one. */
export const locationLabel = (location: { fullCode: string; name?: string }) =>
  location.name ? `${location.fullCode} · ${location.name}` : location.fullCode;

/** A movement kind's badge colour. */
export const movementKindColor = { adjustment: "orange", move: "blue", receipt: "teal" } as const;
