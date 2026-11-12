import { ActionIcon, Autocomplete, Group, NumberInput, Popover, Select, Stack, Text, TextInput } from "@mantine/core";
import { DatePickerInput } from "@mantine/dates";
import { IconCalendar } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { fefoOrder, type InventoryBalanceLot, usableLot } from "../api/lots";
import { variantStockQueryOptions } from "../api/stock";
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
 * serial and how many; on a move a lot the variant has on the row's from
 * location; on an adjustment one of the lots on its location or a new
 * number. The lots are the variant's stock rows (`GET /variants/{id}/stock`),
 * every one of them, per location. Once a move's or an adjustment's location
 * is chosen the field proposes its first usable lot there in FEFO order, as
 * the server's own proposal would; a lot the row names that does not lie on a
 * newly chosen location gives way to that location's proposal.
 */
export const LotField = ({ kind, n, line, siteId, onChange }: LotFieldProps) => {
  const { t, quantity, day } = useInventoryFormat();
  const variant = line.variant;
  const stock = useQuery({
    ...variantStockQueryOptions(variant?.id ?? 0),
    enabled: variant !== undefined && siteId !== undefined,
  });
  const site = stock.data?.sites.find((one) => one.site.id === siteId);
  // A receipt offers the lots anywhere at the site, summed; a move or an adjustment the lots on its location.
  const here = new Map<number, InventoryBalanceLot>();
  for (const row of site?.rows ?? []) {
    if (kind !== "receipt" && row.location.id !== line.locationId) continue;
    for (const lot of row.lots ?? []) {
      const seen = here.get(lot.lotId);
      here.set(lot.lotId, seen ? { ...seen, onHand: seen.onHand + lot.onHand } : lot);
    }
  }
  const ordered = fefoOrder([...here.values()], (lot) => lot.lotId);
  const proposal = kind === "receipt" || line.locationId === null ? undefined : (ordered.find(usableLot) ?? ordered[0]);
  const lotHere = line.lotId !== undefined && here.has(line.lotId);
  // The proposal fills a row once per location chosen, when its lots are read; a row the person empties stays
  // empty until the location changes, and a new number typed on an adjustment is kept.
  const proposedAt = useRef<number | null>(null);
  const ready = stock.data !== undefined;
  useEffect(() => {
    if (kind === "receipt" || !ready || line.locationId === null || proposedAt.current === line.locationId) return;
    proposedAt.current = line.locationId;
    if (lotHere || (line.lotId === undefined && line.lotNumber !== "")) return;
    if (proposal) onChange({ lotId: proposal.lotId, lotNumber: proposal.number });
    else if (line.lotId !== undefined) onChange({ lotId: undefined, lotNumber: "" });
  }, [kind, ready, line.locationId, line.lotId, line.lotNumber, lotHere, proposal, onChange]);
  if (!variant) return null;

  const label = (lot: InventoryBalanceLot) => {
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
        data={ordered.map((lot) => ({ value: String(lot.lotId), label: label(lot) }))}
        value={line.lotId === undefined ? null : String(line.lotId)}
        onChange={(value) => {
          const lot = ordered.find((one) => String(one.lotId) === value);
          onChange({ lotId: lot?.lotId, lotNumber: lot?.number ?? "" });
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
        onChange={(text) => onChange({ lotNumber: text, lotId: byNumber(text)?.lotId })}
      />
      {dates}
    </Group>
  );
};
