import {
  ActionIcon,
  Alert,
  Button,
  Group,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconScan, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection, useI18n } from "@vantigo/frontend-shell";
import { useRef, useState } from "react";
import {
  lookupReceiveCode,
  type PurchasingReceiptLineInput,
  type PurchasingReceiveForm,
  type PurchasingReceiveLine,
  receiveFormQueryOptions,
  receivePurchaseOrder,
} from "../api/purchasing";
import { PURCHASING_QUERY_KEY, refusalCode, refusedLines } from "../api/request";
import { ConfirmDialog } from "../components/common";
import "../i18n";
import { noPurchasingAccess, type PurchasingAccess } from "../lib/access";
import { lineRefusalWords, refusalMessage } from "../lib/errors";
import { newRequestId, usePurchasingFormat } from "../lib/format";
import { readGs1 } from "../lib/gs1";
import { decimal } from "../lib/lines";
import { purchaseOrderLinkOptions, RECEIVING_ROUTE_PATH } from "../lib/routes";

/** One row of the screen: an order line's quantity on one location and, for a tracked variant, one lot. */
interface Row {
  key: string;
  orderLineId: number;
  /**
   * The quantity as typed, in the unit it was typed in: the purchase packaging
   * when `inPackaging`, else the variant's unit. It is what is sent; switching
   * the unit shown only shows it converted, so switching back loses nothing.
   */
  quantity: string;
  inPackaging: boolean;
  /** The unit the field shows: the purchase packaging, or the variant's unit. */
  showInPackaging: boolean;
  locationId: string | null;
  lotNumber: string;
  supplierLot: string;
  productionDate: string;
  bestBefore: string;
  expiry: string;
  /** A serial variant's serials, one per line; their count is the quantity. */
  serials: string;
}

interface Rows {
  rows: Row[];
  /** Whether a scan has been taken: the first one clears every prefilled quantity (counting). */
  counting: boolean;
}

let nextKey = 0;
const rowKey = () => `row-${++nextKey}`;

/** A typed quantity as a number, a comma read as the point; 0 when it is no number. */
const typedNumber = (text: string): number => {
  const n = Number(decimal(text) || 0);
  return Number.isFinite(n) ? n : 0;
};

/** A quantity at most three decimals, as a person types it in their language: no grouping, nb's decimal comma. */
const plain = (value: number, locale: string) => {
  const text = String(Math.round(value * 1000) / 1000);
  return locale.startsWith("nb") ? text.replace(".", ",") : text;
};

const serialList = (text: string) =>
  text
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean);

const blankRow = (line: PurchasingReceiveLine, form: PurchasingReceiveForm): Row => ({
  key: rowKey(),
  orderLineId: line.orderLineId,
  quantity: "0",
  inPackaging: false,
  showInPackaging: false,
  locationId: String(line.defaultLocationId ?? form.locations.find((l) => l.root)?.id ?? form.locations[0]?.id ?? ""),
  lotNumber: "",
  supplierLot: "",
  productionDate: "",
  bestBefore: "",
  expiry: "",
  serials: "",
});

/** Every open goods line, its remainder prefilled in the variant's unit (P24). */
const initialRows = (form: PurchasingReceiveForm): Row[] =>
  form.lines.map((line) => ({ ...blankRow(line, form), quantity: line.tracking === "serial" ? "0" : line.open }));

/** A row's quantity in the variant's unit. */
const unitQuantity = (row: Row, line: PurchasingReceiveLine): number => {
  if (line.tracking === "serial") return serialList(row.serials).length;
  const typed = typedNumber(row.quantity);
  return row.inPackaging ? typed * Number(line.packagingQuantity ?? 1) : typed;
};

/** What a row's field shows: the quantity as typed in the unit shown, else converted to it. */
const shownQuantity = (row: Row, line: PurchasingReceiveLine, locale: string): string => {
  if (row.showInPackaging === row.inPackaging) return row.quantity;
  const units = unitQuantity(row, line);
  return plain(row.showInPackaging ? units / Number(line.packagingQuantity ?? 1) : units, locale);
};

/**
 * The receive screen of a sent order (purchasing design P24–P30, A21): one
 * row per open goods line, its remainder prefilled, with its location and,
 * for a tracked variant, its lot or serials. A row is split for another
 * location or lot; rows at 0 are left out. It shows no prices.
 *
 * A scan field takes what a keyboard-wedge scanner sends, as Inventory's
 * receipt does: the first scan switches the screen to counting — every
 * prefilled quantity is cleared — and each scan adds what one scan of that
 * code is (a packaging's barcode its packaging). A GS1-128 string is read
 * first: its GTIN is looked up and its lot and dates fill the row.
 *
 * One submission is one receipt, idempotent by a request id made once per
 * visit (A11): a refusal of more than ordered asks, then sends the same
 * receipt again with `acceptOver`.
 */
export const ReceivePurchaseOrderPage = ({
  orderId,
  access = noPurchasingAccess,
}: {
  orderId: number;
  access?: PurchasingAccess;
}) => {
  const { t } = usePurchasingFormat();
  const form = useQuery(receiveFormQueryOptions(orderId));
  if (form.isPending) return <ContentSkeleton rows={6} rowHeight={48} />;
  if (form.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {refusalMessage(form.error, t)}
      </Alert>
    );
  return <ReceiveForm form={form.data} access={access} />;
};

const ReceiveForm = ({ form, access }: { form: PurchasingReceiveForm; access: PurchasingAccess }) => {
  const { t, quantity } = usePurchasingFormat();
  const { locale } = useI18n("purchasing");
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [state, setState] = useState<Rows>(() => ({ rows: initialRows(form), counting: false }));
  const [deliveryNote, setDeliveryNote] = useState("");
  const [note, setNote] = useState("");
  const [requestId] = useState(newRequestId);
  const [scan, setScan] = useState("");
  const [scanError, setScanError] = useState<string>();
  const scanRef = useRef<HTMLInputElement>(null);
  const [rowErrors, setRowErrors] = useState<ReadonlyMap<string, string>>(new Map());
  const [overLines, setOverLines] = useState<string[] | null>(null);
  const [error, setError] = useState<string>();
  const lineOf = new Map(form.lines.map((line) => [line.orderLineId, line]));
  const title = form.number ? t("receiveTitle", { number: form.number }) : t("receive");
  const locations = form.locations.map((l) => ({ value: String(l.id), label: l.fullCode }));

  const update = (key: string, change: Partial<Row>) =>
    setState((s) => ({ ...s, rows: s.rows.map((row) => (row.key === key ? { ...row, ...change } : row)) }));
  const split = (row: Row) =>
    setState((s) => {
      const line = lineOf.get(row.orderLineId);
      if (!line) return s;
      const at = s.rows.findIndex((r) => r.key === row.key);
      const added = { ...blankRow(line, form), locationId: row.locationId };
      return { ...s, rows: [...s.rows.slice(0, at + 1), added, ...s.rows.slice(at + 1)] };
    });
  const remove = (key: string) => setState((s) => ({ ...s, rows: s.rows.filter((row) => row.key !== key) }));

  // The field empties as Enter is pressed, so a scanner's next code starts
  // afresh while this one is looked up; each lands on the rows as they are
  // when its answer comes.
  const scanned = async (code: string) => {
    if (!code) return;
    setScan("");
    setScanError(undefined);
    const keep = () => setScan((current) => current || code);
    const gs1 = readGs1(code);
    try {
      const { items } = await lookupReceiveCode(form.orderId, gs1?.gtin ?? code);
      const match = items[0];
      const line = match && lineOf.get(match.orderLineId);
      if (!match || !line) {
        keep();
        setScanError(t("scanNotOnOrder"));
        return;
      }
      // A serial line counts serials: a code without one changes nothing.
      if (line.tracking === "serial" && !gs1?.serial) {
        setScanError(t("scanSerial"));
        return;
      }
      if (line.tracking === "lot" && gs1?.lotUnread) setScanError(t("scanLotUnread"));
      setState((s) => {
        const rows = s.counting
          ? s.rows
          : s.rows.map((row) => (lineOf.get(row.orderLineId)?.tracking === "serial" ? row : { ...row, quantity: "0" }));
        const ofLine = rows.filter((row) => row.orderLineId === line.orderLineId);
        const lot = line.tracking === "lot" ? gs1?.lot : undefined;
        // A lot the code names finds its row; a lot it holds but could not
        // be read goes on a row without a lot, which then asks for it.
        let target = lot
          ? (ofLine.find((row) => row.lotNumber === lot) ??
            ofLine.find((row) => row.lotNumber === "" && typedNumber(row.quantity) === 0))
          : line.tracking === "lot" && gs1?.lotUnread
            ? ofLine.find((row) => row.lotNumber === "")
            : ofLine[0];
        let next = rows;
        if (!target) {
          target = { ...blankRow(line, form), locationId: ofLine[0]?.locationId ?? blankRow(line, form).locationId };
          const last = rows.findLastIndex((row) => row.orderLineId === line.orderLineId);
          next = [...rows.slice(0, last + 1), target, ...rows.slice(last + 1)];
        }
        const change: Partial<Row> = {};
        if (lot) {
          change.lotNumber = lot;
          if (gs1?.productionDate) change.productionDate = gs1.productionDate;
          if (gs1?.bestBefore) change.bestBefore = gs1.bestBefore;
          if (gs1?.expiry) change.expiry = gs1.expiry;
        }
        if (line.tracking === "serial") {
          const serial = gs1?.serial;
          const have = serialList(target.serials);
          if (serial && !have.includes(serial)) change.serials = [...have, serial].join("\n");
        } else {
          // A scan counts in the variant's unit: a row typed in packagings is turned into units first.
          change.inPackaging = false;
          change.showInPackaging = false;
          change.quantity = plain(unitQuantity(target, line) + typedNumber(match.quantity), locale);
        }
        const key = target.key;
        return { counting: true, rows: next.map((row) => (row.key === key ? { ...row, ...change } : row)) };
      });
    } catch (failure) {
      keep();
      setScanError(refusalMessage(failure, t));
    } finally {
      scanRef.current?.focus();
    }
  };

  // The rows sent, each with its row's key, so a refusal's line index finds its row.
  const toSend = () =>
    state.rows.flatMap((row) => {
      const line = lineOf.get(row.orderLineId);
      if (!line || unitQuantity(row, line) <= 0) return [];
      const input: PurchasingReceiptLineInput = {
        orderLineId: row.orderLineId,
        quantity: line.tracking === "serial" ? String(serialList(row.serials).length) : decimal(row.quantity),
        locationId: Number(row.locationId),
      };
      if (row.inPackaging && line.tracking !== "serial") input.inPackaging = true;
      if (line.tracking === "serial") input.serials = serialList(row.serials);
      if (line.tracking === "lot") {
        const lot = {
          number: row.lotNumber.trim(),
          supplierLot: row.supplierLot.trim(),
          productionDate: row.productionDate,
          bestBefore: row.bestBefore,
          expiry: row.expiry,
        };
        const given = Object.fromEntries(Object.entries(lot).filter(([, value]) => value !== ""));
        // A lot left blank is sent as none, which a lot-tracked line refuses as lot_required.
        if (Object.keys(given).length > 0) input.lot = given;
      }
      return [{ key: row.key, input }];
    });
  const sending = toSend();

  const receive = useMutation({
    mutationFn: (acceptOver: boolean) =>
      receivePurchaseOrder(form.orderId, {
        clientRequestId: requestId,
        lines: sending.map((s) => s.input),
        ...(deliveryNote.trim() ? { deliveryNote: deliveryNote.trim() } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
        ...(acceptOver ? { acceptOver: true } : {}),
      }),
    onSuccess: async ({ receipt }) => {
      await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("receiptPosted", { number: receipt.number }) });
      // A receiver who cannot read orders goes back to the orders to receive (A47).
      await navigate(access.canRead ? purchaseOrderLinkOptions(form.orderId) : { to: RECEIVING_ROUTE_PATH });
    },
    onError: (failure) => {
      const lines = refusedLines(failure);
      if (refusalCode(failure) === "over_receipt") {
        setOverLines(
          lines.flatMap((l) => {
            const row = state.rows.find((r) => r.key === sending[l.index]?.key);
            const line = row && lineOf.get(row.orderLineId);
            return line ? [lineName(line)] : [];
          }),
        );
        return;
      }
      setOverLines(null);
      setRowErrors(
        new Map(
          lines.flatMap((l) => {
            const key = sending[l.index]?.key;
            return key ? [[key, lineRefusalWords(l.code, l.detail, t)] as const] : [];
          }),
        ),
      );
      setError(refusalMessage(failure, t));
    },
  });
  const submit = (acceptOver: boolean) => {
    setError(undefined);
    setRowErrors(new Map());
    receive.mutate(acceptOver);
  };

  return (
    <Stack gap="lg">
      <PageHeader title={title} description={`${form.supplierName} · ${form.siteName}`} />
      {form.lines.length === 0 ? (
        <Alert color="gray" variant="light">
          {t("nothingToReceive")}
        </Alert>
      ) : (
        <PageSection>
          <Stack gap="sm">
            <TextInput
              ref={scanRef}
              label={t("scanLabel")}
              description={t("scanHint")}
              placeholder={t("scanPlaceholder")}
              leftSection={<IconScan size={16} />}
              value={scan}
              error={scanError}
              data-assist="purchasing.receive.scan"
              onChange={(event) => {
                setScan(event.currentTarget.value);
                setScanError(undefined);
              }}
              onKeyDown={(event) => {
                if (event.key !== "Enter") return;
                event.preventDefault();
                void scanned(event.currentTarget.value.trim());
              }}
            />
            <Table.ScrollContainer minWidth={900}>
              <Table verticalSpacing="xs" data-testid="receive-rows">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("product")}</Table.Th>
                    <Table.Th>{t("quantity")}</Table.Th>
                    <Table.Th>{t("location")}</Table.Th>
                    <Table.Th>{t("lotOrSerials")}</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {state.rows.map((row, index) => {
                    const line = lineOf.get(row.orderLineId);
                    if (!line) return null;
                    const first = state.rows.findIndex((r) => r.orderLineId === row.orderLineId) === index;
                    const name = lineName(line);
                    const rowError = rowErrors.get(row.key);
                    return (
                      <Table.Tr key={row.key} data-testid="receive-row" data-line={line.orderLineId}>
                        <Table.Td>
                          <Text size="sm">{name}</Text>
                          {first && (
                            <Text size="xs" c="dimmed">
                              {t("receiveOpen", {
                                open: quantity(line.open),
                                ordered: quantity(line.quantity),
                                received: quantity(line.received),
                                unit: line.unit,
                              })}
                            </Text>
                          )}
                          {rowError && (
                            <Text size="xs" c="red" data-testid="row-error">
                              {rowError}
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td>
                          {line.tracking === "serial" ? (
                            <Text size="sm" aria-label={t("quantityOf", { name })}>
                              {`${serialList(row.serials).length} ${line.unit}`}
                            </Text>
                          ) : (
                            <Group gap="xs" wrap="nowrap">
                              <TextInput
                                w={110}
                                inputMode="decimal"
                                aria-label={t("quantityOf", { name })}
                                value={shownQuantity(row, line, locale)}
                                onChange={(e) =>
                                  update(row.key, { quantity: e.currentTarget.value, inPackaging: row.showInPackaging })
                                }
                              />
                              {line.packagingId && line.packagingName && line.packagingQuantity ? (
                                <SegmentedControl
                                  size="xs"
                                  aria-label={t("receiveIn", { name })}
                                  value={row.showInPackaging ? "packaging" : "unit"}
                                  data={[
                                    { value: "unit", label: line.unit },
                                    { value: "packaging", label: line.packagingName },
                                  ]}
                                  onChange={(value) => update(row.key, { showInPackaging: value === "packaging" })}
                                />
                              ) : (
                                <Text size="sm">{line.unit}</Text>
                              )}
                            </Group>
                          )}
                        </Table.Td>
                        <Table.Td>
                          <Select
                            w={180}
                            aria-label={t("locationOf", { name })}
                            data={locations}
                            searchable
                            value={row.locationId}
                            onChange={(value) => update(row.key, { locationId: value })}
                          />
                        </Table.Td>
                        <Table.Td>
                          {line.tracking === "lot" && (
                            <SimpleGrid cols={{ base: 1, md: 3 }} spacing="xs">
                              <TextInput
                                size="xs"
                                label={t("lotNumber")}
                                value={row.lotNumber}
                                onChange={(e) => update(row.key, { lotNumber: e.currentTarget.value })}
                              />
                              <TextInput
                                size="xs"
                                label={t("supplierLot")}
                                value={row.supplierLot}
                                onChange={(e) => update(row.key, { supplierLot: e.currentTarget.value })}
                              />
                              <TextInput
                                size="xs"
                                type="date"
                                label={t("productionDate")}
                                value={row.productionDate}
                                onChange={(e) => update(row.key, { productionDate: e.currentTarget.value })}
                              />
                              <TextInput
                                size="xs"
                                type="date"
                                label={t("bestBefore")}
                                value={row.bestBefore}
                                onChange={(e) => update(row.key, { bestBefore: e.currentTarget.value })}
                              />
                              <TextInput
                                size="xs"
                                type="date"
                                label={t("expiry")}
                                value={row.expiry}
                                onChange={(e) => update(row.key, { expiry: e.currentTarget.value })}
                              />
                            </SimpleGrid>
                          )}
                          {line.tracking === "serial" && (
                            <Textarea
                              size="xs"
                              label={t("serials")}
                              description={t("serialsHint")}
                              autosize
                              minRows={2}
                              value={row.serials}
                              onChange={(e) => update(row.key, { serials: e.currentTarget.value })}
                            />
                          )}
                        </Table.Td>
                        <Table.Td>
                          <Group gap={4} wrap="nowrap">
                            <Button size="compact-xs" variant="subtle" onClick={() => split(row)}>
                              {t("split")}
                            </Button>
                            {!first && (
                              <ActionIcon
                                variant="subtle"
                                color="red"
                                aria-label={t("removeRow")}
                                onClick={() => remove(row.key)}
                              >
                                <IconTrash size={14} />
                              </ActionIcon>
                            )}
                          </Group>
                        </Table.Td>
                      </Table.Tr>
                    );
                  })}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
            <SimpleGrid cols={{ base: 1, sm: 2 }}>
              <TextInput
                label={t("deliveryNote")}
                maxLength={100}
                value={deliveryNote}
                onChange={(e) => setDeliveryNote(e.currentTarget.value)}
              />
              <Textarea
                label={t("note")}
                autosize
                minRows={1}
                maxLength={1000}
                value={note}
                onChange={(e) => setNote(e.currentTarget.value)}
              />
            </SimpleGrid>
          </Stack>
        </PageSection>
      )}
      {error && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} data-testid="receive-error">
          {error}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button
          variant="default"
          renderRoot={(props) =>
            access.canRead ? (
              <Link {...purchaseOrderLinkOptions(form.orderId)} {...props} />
            ) : (
              <Link to={RECEIVING_ROUTE_PATH} {...props} />
            )
          }
        >
          {t("cancel")}
        </Button>
        {access.canReceive && (
          <Button
            loading={receive.isPending}
            disabled={sending.length === 0}
            onClick={() => submit(false)}
            data-assist="purchasing.receive.submit"
          >
            {t("receiveSubmit")}
          </Button>
        )}
      </Group>
      {overLines && (
        <ConfirmDialog
          title={t("overReceiptTitle")}
          body={t("overReceiptBody", { lines: overLines.join(", ") })}
          confirm={t("receiveAnyway")}
          loading={receive.isPending}
          onClose={() => setOverLines(null)}
          onConfirm={() => submit(true)}
        />
      )}
    </Stack>
  );
};

const lineName = (line: PurchasingReceiveLine) => (line.sku ? `${line.sku} · ${line.name}` : line.name);
