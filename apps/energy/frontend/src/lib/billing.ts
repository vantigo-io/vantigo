import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type BillingCadence, ELECTRICITY_TAX_CLASSES, ELECTRICITY_TAX_REASONS } from "../api/energy";
import { type TariffKind, tariffsQueryOptions } from "../api/tariffs";
import "../i18n";

/** The i18n key of a cadence's name: `cadenceMonthly`. */
export const cadenceKey = (cadence: BillingCadence) => `cadence${cadence.charAt(0).toUpperCase()}${cadence.slice(1)}`;

/**
 * The i18n key of a tax class's name: `electricityTaxClassOrdinary`, or
 * `electricityTaxClassUnknown` for a class the client does not know.
 */
export const taxClassKey = (value: string) =>
  (ELECTRICITY_TAX_CLASSES as readonly string[]).includes(value)
    ? `electricityTaxClass${value.charAt(0).toUpperCase()}${value.slice(1)}`
    : "electricityTaxClassUnknown";

/**
 * The i18n key of an electricity-tax suggestion's reason:
 * `electricityTaxReason_industry`, or `electricityTaxReasonUnknown` for a
 * reason the client does not know.
 */
export const taxReasonKey = (reason: string) =>
  (ELECTRICITY_TAX_REASONS as readonly string[]).includes(reason)
    ? `electricityTaxReason_${reason}`
    : "electricityTaxReasonUnknown";

/** `tariffId` as the server takes it. */
export const tariffIdOf = (value: string | null) => (value ? Number(value) : null);

/**
 * The options of a tariff Select: the active tariffs of `kind`, plus the one
 * already held when the list does not have it (it is archived, and the server
 * keeps it). Loads only when `enabled` — the caller holds `energy:tariffs-view`.
 */
export const useTariffOptions = (
  kind: TariffKind,
  enabled: boolean,
  held: { id: number | null; name: string | null },
) => {
  const { t } = useI18n("energy");
  const { data } = useQuery({ ...tariffsQueryOptions({ kind, status: "active" }), enabled });
  const options = (data ?? []).map((tariff) => ({ value: String(tariff.id), label: tariff.name }));
  if (held.id !== null && !options.some((option) => option.value === String(held.id)))
    options.push({
      value: String(held.id),
      label: t("tariffHeldArchived", { name: held.name ?? t("tariffNumber", { id: held.id }) }),
    });
  return options;
};
