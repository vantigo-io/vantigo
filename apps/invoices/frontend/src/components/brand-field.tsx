import { Alert, Select, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type InvoiceBrand, invoiceBrandsQueryOptions } from "../api/brands";
import "../i18n";

/**
 * The editor's Brand field (brands design B28, B29a): the brand this one
 * document is printed in, switchable for a one-time document. `current` is the
 * brand the draft carries; `chosen` the id the person picked in this session,
 * null while they have not touched it, so a save sends `brandId` only for a real
 * choice and the server keeps the draft's source otherwise.
 *
 * A brand archived since it was picked, or a customer's default that was
 * skipped, is said so in a notice: the issue falls back to the next brand in
 * the order. The field says too that the seller's legal identity is one.
 */
export const BrandField = ({
  current,
  chosen,
  disabled,
  onChange,
}: {
  current: InvoiceBrand | undefined;
  chosen: string | null;
  disabled: boolean;
  onChange: (brandId: string) => void;
}) => {
  const { t } = useI18n("invoices");
  const { data: options } = useQuery(invoiceBrandsQueryOptions());
  const labels = new Map<string, string>();
  for (const option of options ?? []) labels.set(option.id, option.name);
  // The draft's own brand widens the list: one archived since it was picked is
  // no longer offered but must stay readable, labelled as archived.
  if (current) {
    const name = current.name ?? "";
    labels.set(
      current.id,
      current.unavailable ? t("brand.archivedOption", { name }) : (labels.get(current.id) ?? name),
    );
  }
  if (labels.size === 0) return null;
  const value = chosen ?? current?.id ?? null;
  const notice = chosen === null && current?.unavailable === true;

  return (
    <>
      <Select
        label={t("brand.label")}
        description={t("brand.hint")}
        allowDeselect={false}
        data={[...labels].map(([id, label]) => ({ value: id, label }))}
        value={value}
        disabled={disabled}
        onChange={(id) => id && onChange(id)}
      />
      {notice && (
        <Alert color="yellow" data-testid="brand-notice">
          <Text size="sm">{t("brand.notice")}</Text>
        </Alert>
      )}
    </>
  );
};
