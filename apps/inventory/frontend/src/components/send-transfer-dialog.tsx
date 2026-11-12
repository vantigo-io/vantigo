import { Alert, Button, Checkbox, Group, Modal, Select, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueries } from "@tanstack/react-query";
import { useState } from "react";
import { fefoOrder, type InventoryBalanceLot, usableLot } from "../api/lots";
import { refusalCodes } from "../api/movements";
import { isRevisionConflict, refusalCode, refusedLines } from "../api/request";
import { type InventoryVariantStock, variantStockQueryOptions } from "../api/stock";
import { type InventoryTransfer, sendTransfer } from "../api/transfers";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";
import { stockAtKey, useStockAtSites } from "../lib/stock-at-sites";
import { useTransferActions } from "../lib/transfer-actions";
import { LocationPicker } from "./location-picker";

export interface SendTransferDialogProps {
  opened: boolean;
  /** Called with the transfer as sent, or with nothing when the dialog was closed without sending. */
  onClose: (sent?: InventoryTransfer) => void;
  /** A saved draft, at the revision the send is guarded by. */
  transfer: InventoryTransfer;
  /** Send and receive (I43): a destination at the to site for every line, received in the same step. */
  withReceive?: boolean;
}

type Marks = ReadonlyMap<number, string>;

/**
 * Sending a draft (I39): a source location at the from site for every line,
 * defaulted to its primary location there when that holds some, else to the
 * location holding most of it (I53, I55); a warning, never a
 * refusal, when a variant takes more than is available because orders have
 * reserved it; and whether to print the følgeseddel once it is sent (I51).
 * With `withReceive` each line also gets its destination at the to site and
 * everything sent is received at once (I43). The send's key is derived from
 * the transfer on the server, so a retry replays.
 *
 * A tracked item's line sends one lot (manufacturing phase 2 M70): the
 * draft's when it lies on the source location, else the first usable lot
 * there in FEFO order — the server's own order — until the person picks
 * another; the lots are the variant's stock rows at the from site.
 */
export const SendTransferDialog = ({ opened, onClose, transfer, withReceive }: SendTransferDialogProps) => {
  const { t, quantity, day } = useInventoryFormat();
  const invalidate = useInvalidateInventory();
  const { printNote } = useTransferActions();
  const lines = transfer.lines;
  const stock = useStockAtSites(
    lines.map((line) => line.variant.id),
    withReceive ? [transfer.fromSite.id, transfer.toSite.id] : [transfer.fromSite.id],
  );
  const [sources, setSources] = useState<Record<number, number | null>>({});
  const [destinations, setDestinations] = useState<Record<number, number | null>>({});
  const [chosenLots, setChosenLots] = useState<Record<number, number | null>>({});
  const trackedIds = [
    ...new Set(lines.filter((line) => (line.variant.tracking ?? "none") !== "none").map((line) => line.variant.id)),
  ];
  const lotStock = useQueries({
    queries: trackedIds.map((id) => variantStockQueryOptions(id)),
    combine: (results) => {
      const out = new Map<number, InventoryVariantStock>();
      for (const result of results) if (result.data) out.set(result.data.variant.id, result.data);
      return out;
    },
  });
  const [print, setPrint] = useState(!withReceive);
  const [marks, setMarks] = useState<Marks>(new Map());
  const [problem, setProblem] = useState<string>();

  const at = (variantId: number) => stock.data?.get(stockAtKey(variantId, transfer.fromSite.id));
  // The variant's primary location at the from site is the default when it holds some, else the location holding
  // most (I53, I55), until the person picks another.
  const defaultSource = (variantId: number): number | null => {
    const here = at(variantId);
    const primary = here?.locations.find((one) => one.primary || one.location.id === here.placement?.primaryLocationId);
    return (primary ?? here?.locations[0])?.location.id ?? null;
  };
  const sourceOf = (lineId: number, variantId: number): number | null =>
    sources[lineId] !== undefined ? sources[lineId] : defaultSource(variantId);
  // Send and receive puts each line on its primary location at the to site until a destination is chosen (I55).
  const destinationOf = (lineId: number, variantId: number): number | null =>
    destinations[lineId] !== undefined
      ? destinations[lineId]
      : (stock.data?.get(stockAtKey(variantId, transfer.toSite.id))?.placement?.primaryLocationId ?? null);

  // A tracked line's lots on its source location, FEFO, and the one it sends.
  const lotsAt = (variantId: number, locationId: number | null): InventoryBalanceLot[] => {
    const site = lotStock.get(variantId)?.sites.find((one) => one.site.id === transfer.fromSite.id);
    const row = site?.rows.find((one) => one.location.id === locationId);
    return fefoOrder(row?.lots ?? [], (lot) => lot.lotId);
  };
  const tracked = (line: InventoryTransfer["lines"][number]) => (line.variant.tracking ?? "none") !== "none";
  const lotOf = (line: InventoryTransfer["lines"][number]): number | null => {
    if (!tracked(line)) return null;
    if (chosenLots[line.id] !== undefined) return chosenLots[line.id];
    const here = lotsAt(line.variant.id, sourceOf(line.id, line.variant.id));
    if (line.lot && (here.length === 0 || here.some((lot) => lot.lotId === line.lot?.id))) return line.lot.id;
    return (here.find(usableLot) ?? here[0])?.lotId ?? null;
  };

  // What the transfer takes of each variant, every line of it together.
  const wanted = new Map<number, number>();
  for (const line of lines) wanted.set(line.variant.id, (wanted.get(line.variant.id) ?? 0) + line.quantity);
  const warnings = [...wanted].flatMap(([variantId, total]) => {
    const here = at(variantId);
    const variant = lines.find((line) => line.variant.id === variantId)?.variant;
    if (!here || !variant) return [];
    const values = { sku: variant.sku, site: transfer.fromSite.code };
    if (total > here.onHand)
      return [
        {
          key: variantId,
          short: true,
          text: t("sendMoreThanOnHand", { ...values, onHand: quantity(here.onHand, variant.unit) }),
        },
      ];
    if (total > here.available && here.reserved > 0)
      return [
        {
          key: variantId,
          short: false,
          text: t("sendTakesReserved", { ...values, reserved: quantity(here.reserved, variant.unit) }),
        },
      ];
    return [];
  });

  const sending = useMutation({
    mutationFn: () =>
      sendTransfer(
        transfer.id,
        {
          lines: lines.map((line) => ({
            lineId: line.id,
            ...(sourceOf(line.id, line.variant.id) === null
              ? {}
              : { fromLocationId: sourceOf(line.id, line.variant.id) ?? 0 }),
            ...(lotOf(line) === null ? {} : { lotId: lotOf(line) ?? 0 }),
          })),
          ...(withReceive
            ? {
                receive: {
                  lines: lines.map((line) => ({
                    lineId: line.id,
                    toLocationId: destinationOf(line.id, line.variant.id) ?? 0,
                  })),
                },
              }
            : {}),
        },
        transfer.revision,
      ),
    onSuccess: async (sent) => {
      notifications.show({
        color: "teal",
        title: withReceive ? t("transferSentAndReceived") : t("transferSent"),
        message: t("transferNumber", { number: sent.number }),
      });
      await invalidate();
      if (print) await printNote(sent);
      onClose(sent);
    },
    onError: async (error) => {
      // Whatever the refusal, the stock and the transfer are read again: the defaults and warnings follow them.
      await invalidate();
      if (isRevisionConflict(error) || refusalCode(error) === "transfer_not_draft") {
        // The draft moved on since it was read: the dialog closes on the transfer as it is now.
        notifications.show({
          color: "yellow",
          title: t("transferChanged"),
          message: isRevisionConflict(error) ? t("transferChangedHint") : t("refusal.transfer_not_draft"),
        });
        onClose();
        return;
      }
      const failing = refusedLines(error);
      const next = new Map<number, string>();
      for (const line of failing) {
        const row = lines[line.index];
        if (!row) continue;
        next.set(
          row.id,
          (refusalCodes as readonly string[]).includes(line.code) ? t(`refusal.${line.code}`) : line.detail,
        );
      }
      setMarks(next);
      setProblem(failing.length > 0 ? t("linesRefused", { count: failing.length }) : refusalMessage(t, error));
    },
  });

  const submit = () => {
    const gaps = new Map<number, string>();
    for (const line of lines) {
      if (sourceOf(line.id, line.variant.id) === null) gaps.set(line.id, t("refusal.from_location_required"));
      else if (tracked(line) && lotOf(line) === null) gaps.set(line.id, t("refusal.lot_required"));
      else if (withReceive && !destinationOf(line.id, line.variant.id)) gaps.set(line.id, t("pickDestination"));
    }
    setMarks(gaps);
    if (gaps.size > 0) {
      setProblem(t("linesIncomplete"));
      return;
    }
    setProblem(undefined);
    sending.mutate();
  };

  return (
    <Modal
      opened={opened}
      onClose={() => onClose()}
      size="xl"
      title={
        withReceive
          ? t("sendAndReceiveTitle", { number: transfer.number, site: transfer.toSite.code })
          : t("sendTitle", { number: transfer.number, site: transfer.toSite.code })
      }
    >
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {t("sendHint", { site: `${transfer.fromSite.code} · ${transfer.fromSite.name}` })}
        </Text>
        {stock.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
            {stock.error.message}
          </Alert>
        )}
        {withReceive && (
          <LocationPicker
            siteId={transfer.toSite.id}
            label={t("receiveAllAt", { site: transfer.toSite.code })}
            value={null}
            onChange={(locationId) =>
              locationId !== null && setDestinations(Object.fromEntries(lines.map((line) => [line.id, locationId])))
            }
          />
        )}
        <Table.ScrollContainer minWidth={560}>
          <Table verticalSpacing="xs">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("columnItem")}</Table.Th>
                <Table.Th ta="right">{t("columnQuantity")}</Table.Th>
                <Table.Th>{t("sendFrom", { site: transfer.fromSite.code })}</Table.Th>
                {trackedIds.length > 0 && <Table.Th>{t("columnLot")}</Table.Th>}
                {withReceive && <Table.Th>{t("receiveTo", { site: transfer.toSite.code })}</Table.Th>}
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {lines.map((line) => {
                const here = at(line.variant.id);
                const source = sourceOf(line.id, line.variant.id);
                const onSource = here?.locations.find((one) => one.location.id === source)?.onHand;
                const mark = marks.get(line.id);
                return (
                  <Table.Tr key={line.id} data-refused={mark ? "true" : undefined}>
                    <Table.Td>
                      <Text size="sm">
                        {line.variant.sku} · {line.variant.productName}
                      </Text>
                      {mark && (
                        <Text size="xs" c="red" fw={500}>
                          {mark}
                        </Text>
                      )}
                      {onSource !== undefined && onSource < line.quantity && (
                        <Text size="xs" c="orange">
                          {t("sendMoreThanLocation", { onHand: quantity(onSource, line.variant.unit) })}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td ta="right">{quantity(line.quantity, line.variant.unit)}</Table.Td>
                    <Table.Td miw={220}>
                      <Select
                        aria-label={t("sourceOfLine", { n: line.lineNo })}
                        placeholder={here && here.locations.length === 0 ? t("nothingAtSite") : t("pickLocation")}
                        searchable
                        data={(here?.locations ?? []).map((one) => ({
                          value: String(one.location.id),
                          label: `${one.location.fullCode}${one.primary ? ` (${t("primary")})` : ""} · ${quantity(one.onHand)}`,
                        }))}
                        value={source === null ? null : String(source)}
                        onChange={(next) => {
                          setSources((current) => ({ ...current, [line.id]: next === null ? null : Number(next) }));
                          // Another location proposes its own lot.
                          setChosenLots(({ [line.id]: _, ...rest }) => rest);
                        }}
                        disabled={stock.isPending}
                      />
                    </Table.Td>
                    {trackedIds.length > 0 && (
                      <Table.Td miw={200}>
                        {tracked(line) && (
                          <Select
                            aria-label={t("lotOfLine", { n: line.lineNo })}
                            placeholder={t("lotPickPlaceholder")}
                            searchable
                            data={lotsAt(line.variant.id, source).map((lot) => ({
                              value: String(lot.lotId),
                              label: [
                                lot.number,
                                lot.expiry ? t("lotExpires", { date: day(lot.expiry) }) : "",
                                quantity(lot.onHand, line.variant.unit),
                                usableLot(lot) ? "" : lot.expired ? t("expired") : t(`lotStatus_${lot.status}`),
                              ]
                                .filter(Boolean)
                                .join(" · "),
                            }))}
                            value={lotOf(line) === null ? null : String(lotOf(line))}
                            onChange={(next) =>
                              setChosenLots((current) => ({
                                ...current,
                                [line.id]: next === null ? null : Number(next),
                              }))
                            }
                          />
                        )}
                      </Table.Td>
                    )}
                    {withReceive && (
                      <Table.Td miw={200}>
                        <LocationPicker
                          compact
                          required
                          siteId={transfer.toSite.id}
                          label={t("destinationOfLine", { n: line.lineNo })}
                          value={destinationOf(line.id, line.variant.id)}
                          onChange={(locationId) =>
                            setDestinations((current) => ({ ...current, [line.id]: locationId }))
                          }
                        />
                      </Table.Td>
                    )}
                  </Table.Tr>
                );
              })}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        {warnings.map((warning) => (
          <Alert
            key={warning.key}
            color={warning.short ? "orange" : "yellow"}
            variant="light"
            icon={<IconAlertTriangle size={16} />}
          >
            {warning.text}
          </Alert>
        ))}
        <Checkbox
          label={t("printNoteAfterSend")}
          checked={print}
          onChange={(event) => setPrint(event.currentTarget.checked)}
        />
        {problem && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSend")}>
            {problem}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={() => onClose()}>
            {t("cancel")}
          </Button>
          <Button onClick={submit} loading={sending.isPending}>
            {withReceive ? t("sendAndReceiveLines", { count: lines.length }) : t("sendLines", { count: lines.length })}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
