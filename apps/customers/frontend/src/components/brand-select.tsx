import { Select } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type BrandChoice, brandOptionsQueryOptions } from "../api/brands";
import "../i18n";

/**
 * The value of the first row, "use the next default": a Mantine `Select` needs a
 * real string among its `data` to offer a row at all, and "no brand of its own"
 * is a choice a person makes rather than a cleared field (the group picker's
 * `NO_GROUP`, the same reason).
 */
const NO_BRAND = "";

/**
 * A brand picker for a customer or a group (brands design B28): the published
 * brands with a first row that clears the choice, so the customer takes its
 * group's brand and the group the workspace default.
 *
 * The CURRENT choice seeds the option list and the published brands widen it,
 * for the group picker's reason: a `Select` renders nothing for a value its
 * `data` does not describe, and a brand archived after it was chosen is no
 * longer offered but must still be readable — it is labelled "(archived)", so
 * saving the form again does not silently drop it and the person sees why the
 * default fell back.
 */
export const BrandSelect = ({
  label,
  emptyLabel,
  current,
  disabled,
  error,
  onChange,
}: {
  label: string;
  /** What the first row says: what happens when the field is left empty. */
  emptyLabel: string;
  current: BrandChoice | null;
  disabled?: boolean;
  error?: string;
  onChange: (brandId: string | null) => void;
}) => {
  const { t } = useI18n("customers");
  const { data: options } = useQuery(brandOptionsQueryOptions());
  const labels = new Map<string, string>();
  if (current) {
    const name = current.name ?? t("brandUnavailable");
    labels.set(current.id, current.state === "published" ? name : t("brandArchivedOption", { name }));
  }
  for (const option of options ?? []) labels.set(option.id, option.name);

  return (
    <Select
      label={label}
      allowDeselect={false}
      data={[{ value: NO_BRAND, label: emptyLabel }, ...[...labels].map(([value, text]) => ({ value, label: text }))]}
      value={current?.id ?? NO_BRAND}
      disabled={disabled}
      error={error}
      onChange={(value) => onChange(value === NO_BRAND || value === null ? null : value)}
    />
  );
};
