import { useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { ApiValidationError, MANUFACTURING_QUERY_KEY, refusalCode, refusalCodes, refusedPath } from "../api/request";
import { formatDecimal, roundDecimal } from "./decimal";
import "../i18n";

type TFunction = ReturnType<typeof useI18n>["t"];

/** The package's words and its ways of showing decimals, amounts and instants, in the viewer's language. */
export const useManufacturingFormat = () => {
  const { t, locale, formatters } = useI18n("manufacturing");
  const decimal = (text: string) => formatDecimal(text, locale);
  return {
    t,
    locale,
    /** Exact decimal text with every digit kept: `1 234,5`. */
    decimal,
    /** A quantity in its unit: `2,5 pcs`. */
    quantity: (text: string, unit?: string) => (unit ? `${decimal(text)} ${unit}` : decimal(text)),
    /** An amount in NOK, rounded half-up to øre at display only (§15). */
    money: (text: string, currency = "NOK") => `${formatDecimal(roundDecimal(text, 2), locale)} ${currency}`,
    /** Whole minutes: `95 min`. */
    minutes: (value: number) => t("minutesValue", { value: formatters.formatNumber(value) }),
    /** An instant, day and time. */
    stamp: (instant: string) => formatters.formatDate(instant, { dateStyle: "medium", timeStyle: "short" }),
    /** A calendar date (`2026-11-12`), as the day it names wherever the viewer is. */
    day: (date: string) => formatters.formatDate(`${date}T00:00:00Z`, { dateStyle: "medium", timeZone: "UTC" }),
  };
};

/**
 * The words for a refusal: the module's own for a code it answers
 * (`refusal.<code>`, a cycle with its path of SKUs), else what the server
 * said — a field error's first message, or the problem's detail. A failure
 * with no HTTP status never reached the server and says so; an answer
 * without words of its own says its status.
 */
export const refusalMessage = (t: TFunction, error: Error): string => {
  const status = (error as { status?: unknown }).status;
  if (typeof status !== "number") return t("couldNotReach");
  const code = refusalCode(error);
  if (code === "bom_cycle") {
    const path = refusedPath(error);
    if (path.length > 0) return t("refusal.bom_cycle_path", { path: path.map((step) => step.sku).join(" → ") });
  }
  if (code && (refusalCodes as readonly string[]).includes(code)) return t(`refusal.${code}`);
  if (error instanceof ApiValidationError) {
    const first = Object.values(error.fieldErrors)[0];
    if (first) return first;
  }
  return error.message || t("requestFailed", { status });
};

/** Every write invalidates the whole package: a BoM or a work center saved changes lists, stats and overviews at once. */
export const useInvalidateManufacturing = () => {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY] });
};

/** How a variant reads in a picker and a row: its SKU, then its product. */
export const variantLabel = (variant: { sku: string; productName: string }) =>
  `${variant.sku} · ${variant.productName}`;

/** A status's badge colour. */
export const statusColor = { active: "teal", archived: "gray" } as const;
