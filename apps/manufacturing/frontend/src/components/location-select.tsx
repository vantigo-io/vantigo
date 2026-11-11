import { Select } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { siteLocationsQueryOptions } from "../api/locations";

export interface LocationSelectProps {
  label: string;
  siteId: number | undefined;
  value: number | null;
  onChange: (locationId: number | null) => void;
  description?: string;
  placeholder?: string;
  clearable?: boolean;
  disabled?: boolean;
  error?: string;
  /** Shown without a label, as in a line's row. */
  compact?: boolean;
}

/**
 * A location of the site (`GET /sites/{id}/locations`), by full code: the
 * active ones are offered (location_unknown otherwise), and the one chosen
 * is still named when it has been archived since.
 */
export const LocationSelect = ({
  label,
  siteId,
  value,
  onChange,
  description,
  placeholder,
  clearable,
  disabled,
  error,
  compact,
}: LocationSelectProps) => {
  const locations = useQuery({ ...siteLocationsQueryOptions(siteId ?? 0), enabled: siteId !== undefined });
  const options = (locations.data ?? [])
    .filter((location) => location.active || location.id === value)
    .map((location) => ({ value: String(location.id), label: location.fullCode }));
  return (
    <Select
      label={compact ? undefined : label}
      aria-label={label}
      description={compact ? undefined : description}
      placeholder={placeholder}
      searchable
      clearable={clearable}
      disabled={disabled || siteId === undefined}
      data={options}
      value={value === null ? null : String(value)}
      onChange={(next) => onChange(next === null ? null : Number(next))}
      error={error}
    />
  );
};
