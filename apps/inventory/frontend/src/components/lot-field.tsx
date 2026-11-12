import { ActionIcon, Autocomplete, Group, NumberInput, Popover, Select, Stack, Text, TextInput } from "@mantine/core";
import { DatePickerInput } from "@mantine/dates";
import { IconCalendar } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { fefoOrder, type InventoryLotSummary, usableLot, variantLotsQueryOptions } from "../api/lots";
import { useInventoryFormat } from "../lib/format";
import { type EditorLine, type LineEditorKind, takesSerials } from "../lib/lines";

/** At most this many serials expand from one receipt line (M57). */
const MAX_SERIALS = 500;

export interface LotFieldProps {
  kind: LineEditorKind;
  /** The row's number, for the labels. */
  n: number;
  line: EditorLine;
  siteId: number | undefined;
  onChange: (change: Partial<EditorLine>) => void;
}

/**
 * A tracked row's lot (manufacturing phase 2 M59): on a receipt the lot's
 * number, picked from the variant's lots at the site or typed — left empty,
 * one is made — with its dates; on a receipt of a serial variant the first
 * serial and how many; on a move a lot the variant has at the site; on an
 * adjustment one of those or a new number. A move or an adjustment proposes
 * the first usable lot in FEFO order, as the server's own proposal would.
 */
export const LotField = ({ kind, n, line, siteId, onChange }: LotFieldProps) => {
  const { t, quantity, day } = useInventoryFormat();
  const variant = line.variant;
  const lots = useQuery({
    ...variantLotsQueryOptions(variant?.id ?? 0, siteId),
    enabled: variant !== undefined && siteId !== undefined,
  });
  const ordered = fefoOrder(lots.data ?? []);
  const proposal = kind === "receipt" ? undefined : (ordered.find(usableLot) ?? ordered[0]);
  // The proposal fills an untouched row once, when the lots are read; a row the person empties stays empty.
  const proposed = useRef(false);
  useEffect(() => {
    if (proposed.current || !proposal) return;
    proposed.current = true;
    if (line.lotId === undefined && line.lotNumber === "") onChange({ lotId: proposal.id, lotNumber: proposal.number });
  }, [proposal, line.lotId, line.lotNumber, onChange]);
  if (!variant) return null;

  const label = (lot: InventoryLotSummary) => {
    const text = lot.expiry
      ? t("lotExpiresOption", {
          number: lot.number,
          date: day(lot.expiry),
          quantity: quantity(lot.onHand, variant.unit),
        })
      : t("lotOnHandOption", { number: lot.number, quantity: quantity(lot.onHand, variant.unit) });
    if (lot.expired) return `${text} · ${t("expired")}`;
    return lot.status === "available" ? text : `${text} · ${t(`lotStatus_${lot.status}`)}`;
  };
  const dates = (
    <Popover position="bottom-end" withArrow shadow="md">
      <Popover.Target>
        <ActionIcon variant="subtle" aria-label={t("lotDatesOfLine", { n })} title={t("lotDates")}>
          <IconCalendar size={16} />
        </ActionIcon>
      </Popover.Target>
      <Popover.Dropdown>
        <Stack gap="xs" maw={260}>
          <Text size="xs" c="dimmed">
            {t("lotDatesHint")}
          </Text>
          {(["productionDate", "bestBefore", "expiry"] as const).map((field) => (
            <DatePickerInput
              key={field}
              size="xs"
              clearable
              dropdownType="modal"
              valueFormat={t("dateInputFormat")}
              label={t(field === "expiry" ? "expiryDate" : field)}
              value={line.lotDates[field] || null}
              onChange={(next) => onChange({ lotDates: { ...line.lotDates, [field]: next ?? "" } })}
            />
          ))}
        </Stack>
      </Popover.Dropdown>
    </Popover>
  );

  if (takesSerials(kind, variant))
    return (
      <Group gap={4} wrap="nowrap" align="flex-start">
        <Stack gap={4}>
          <TextInput
            size="xs"
            aria-label={t("serialsOfLine", { n })}
            placeholder={t("serialFirst")}
            value={line.serialFirst}
            maxLength={20}
            onChange={(event) => onChange({ serialFirst: event.currentTarget.value })}
          />
          <NumberInput
            size="xs"
            aria-label={t("serialCount")}
            placeholder={t("serialCount")}
            min={1}
            max={MAX_SERIALS}
            allowDecimal={false}
            allowNegative={false}
            value={line.serialCount}
            onChange={(value) => {
              const count = value === "" ? "" : Math.min(MAX_SERIALS, Math.max(1, Math.trunc(Number(value))));
              onChange({ serialCount: count, quantity: count });
            }}
          />
          <Text size="xs" c="dimmed">
            {t("serialHint")}
          </Text>
        </Stack>
        {dates}
      </Group>
    );

  if (kind === "move")
    return (
      <Select
        size="xs"
        searchable
        aria-label={t("lotOfLine", { n })}
        placeholder={t("lotPickPlaceholder")}
        data={ordered.map((lot) => ({ value: String(lot.id), label: label(lot) }))}
        value={line.lotId === undefined ? null : String(line.lotId)}
        onChange={(value) => {
          const lot = ordered.find((one) => String(one.id) === value);
          onChange({ lotId: lot?.id, lotNumber: lot?.number ?? "" });
        }}
      />
    );

  // A receipt or an adjustment: a lot of the variant by number, or a new number.
  const byNumber = (text: string) => ordered.find((lot) => lot.number.toLowerCase() === text.trim().toLowerCase());
  return (
    <Group gap={4} wrap="nowrap">
      <Autocomplete
        size="xs"
        aria-label={t("lotOfLine", { n })}
        placeholder={kind === "receipt" ? t("lotNewPlaceholder") : t("lotTypePlaceholder")}
        maxLength={20}
        data={ordered.map((lot) => ({ value: lot.number, label: label(lot) }))}
        value={line.lotNumber}
        onChange={(text) => onChange({ lotNumber: text, lotId: byNumber(text)?.id })}
      />
      {dates}
    </Group>
  );
};
