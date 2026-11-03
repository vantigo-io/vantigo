import { Select } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { siteLocationsQueryOptions } from "../api/locations";
import { locationLabel, useInventoryFormat } from "../lib/format";

export interface LocationPickerProps {
  /** The site whose locations are offered; nothing is offered without one. */
  siteId: number | undefined;
  label: string;
  value: number | null;
  onChange: (locationId: number | null) => void;
  error?: string;
  required?: boolean;
  placeholder?: string;
  /** Locations that may not be picked: a location's own subtree, when it is re-parented. */
  exclude?: ReadonlySet<number>;
  /** Shown without a label, as in a line editor's row. */
  compact?: boolean;
}

/**
 * A location of a site, picked by full code (`OSL-A-03-2`) or name. Only active
 * locations are offered (an archived one takes no stock, I6); the site's
 * flat list is one request, shared with the site page's tree.
 */
export const LocationPicker = ({
  siteId,
  label,
  value,
  onChange,
  error,
  required,
  placeholder,
  exclude,
  compact,
}: LocationPickerProps) => {
  const { t } = useInventoryFormat();
  const locations = useQuery({
    ...siteLocationsQueryOptions(siteId ?? 0, "active"),
    enabled: siteId !== undefined,
  });
  const data = (locations.data ?? [])
    .filter((location) => !exclude?.has(location.id))
    .map((location) => ({
      value: String(location.id),
      label: locationLabel(location),
    }));
  return (
    <Select
      label={compact ? undefined : label}
      aria-label={label}
      placeholder={placeholder ?? t("pickLocation")}
      searchable
      clearable={!required}
      withAsterisk={required && !compact}
      data={data}
      value={value === null ? null : String(value)}
      onChange={(next) => onChange(next === null ? null : Number(next))}
      nothingFoundMessage={t("noLocationsFound")}
      error={error}
      disabled={siteId === undefined}
    />
  );
};
