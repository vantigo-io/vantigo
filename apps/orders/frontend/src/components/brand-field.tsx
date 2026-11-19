import { Alert, Select, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { type Brand, brandsQueryOptions } from "../api/brands";
import { useOrdersFormat } from "../lib/format";

/**
 * The editor's Brand field (brands design B28, B29a): the brand this one
 * document is printed in, switchable for a one-time document. `current` is the
 * brand the document carries; `chosen` the id the person picked in this
 * session, null while they have not touched it, so a save sends `brandId` only
 * for a real choice and the server keeps the draft's source otherwise. A sent
 * quote's and a confirmed order's brand is the snapshot and is read-only.
 *
 * A brand archived since it was picked, or a customer's default that was
 * skipped, is said so in a notice: the send or the confirm falls back to the
 * next brand in the order. The field says too that the seller's legal identity
 * is one.
 */
export const BrandField = ({
  current,
  chosen,
  disabled,
  onChange,
}: {
  current: Brand | undefined;
  chosen: string | null;
  disabled: boolean;
  onChange: (brandId: string) => void;
}) => {
  const { t } = useOrdersFormat();
  const { data: options } = useQuery(brandsQueryOptions());
  const labels = new Map<string, string>();
  for (const option of options ?? []) labels.set(option.id, option.name);
  // The document's own brand widens the list: one archived since it was
  // picked is no longer offered but must stay readable, labelled as archived.
  // A snapshot (disabled) is read as it was and is never called archived, and
  // while the list loads nothing is known to be gone.
  let gone = false;
  if (current) {
    const name = current.name ?? labels.get(current.id) ?? "";
    gone = !disabled && (current.unavailable || (options !== undefined && !labels.has(current.id)));
    labels.set(current.id, gone ? t("brandArchivedOption", { name }) : name);
  }
  if (labels.size === 0) return null;
  const value = chosen ?? current?.id ?? null;
  const notice = chosen === null && gone;

  return (
    <>
      <Select
        label={t("brand")}
        description={t("brandHint")}
        allowDeselect={false}
        w={320}
        data={[...labels].map(([id, label]) => ({ value: id, label }))}
        value={value}
        disabled={disabled}
        onChange={(id) => id && onChange(id)}
      />
      {notice && (
        <Alert color="yellow" data-testid="brand-notice">
          <Text size="sm">{t("brandNotice")}</Text>
        </Alert>
      )}
    </>
  );
};
