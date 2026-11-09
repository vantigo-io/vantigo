import { Alert, Anchor, Button, Group, NumberInput, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconChecks, IconScan } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useBlocker, useNavigate, useParams } from "@tanstack/react-router";
import { useWindowVirtualizer } from "@tanstack/react-virtual";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useRef, useState } from "react";
import { lookupQueryOptions } from "../api/lookup";
import { inventoryMetaQueryOptions } from "../api/meta";
import { refusalCodes } from "../api/movements";
import { refusalCode, refusedLines } from "../api/request";
import { siteQueryOptions } from "../api/sites";
import {
  type InventoryTransfer,
  type InventoryTransferCloseReason,
  type InventoryTransferLine,
  type InventoryTransferReceiveLine,
  receiveTransfer,
  transferQueryOptions,
} from "../api/transfers";
import { CloseDifferenceDialog } from "../components/close-difference-dialog";
import { LocationPicker } from "../components/location-picker";
import { PackagingSteppers } from "../components/packaging-steppers";
import { packedQuantity } from "../lib/breakdown";
import { canTransfer } from "../lib/capabilities";
import { QUANTITY_DECIMALS, refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";
import { enteredOf, isPacked, roundQuantity } from "../lib/lines";
import {
  INVENTORY_HOME_ROUTE_PATH,
  receiveTransferLinkOptions,
  TRANSFERS_ROUTE_PATH,
  transferLinkOptions,
} from "../lib/routes";
import { askToLeave, isReceivable } from "../lib/transfer-actions";

/** A line's row is drawn at about this height before it is measured. */
const LINE_HEIGHT_ESTIMATE = 64;
/** Lines drawn beyond the window, above and below. */
const LINES_OVERSCAN = 10;

/** What has arrived of one line, as the receiver counts it. */
interface Arrived {
  quantity: number | "";
  packs: Readonly<Record<number, number>>;
}
const none: Arrived = { quantity: "", packs: {} };

/** What the count stands for in the variant's unit; 0 while nothing is counted. */
const arrivedTotal = (line: InventoryTransferLine, arrived: Arrived) =>
  isPacked(arrived)
    ? packedQuantity(line.variant.packagings ?? [], arrived.packs, Number(arrived.quantity || 0))
    : Number(arrived.quantity || 0);

/** The receive page's body: the lines that arrived now, and the transfer the page was opened on. */
const ReceiveLines = ({ transfer, rootLocationId }: { transfer: InventoryTransfer; rootLocationId?: number }) => {
  const { t, quantity } = useInventoryFormat();
  const queryClient = useQueryClient();
  const invalidate = useInvalidateInventory();
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  // Made when the page opens and kept across retries (D I14): two receivers each send their own (I41).
  const [clientRequestId, setClientRequestId] = useState(() => crypto.randomUUID());
  const [arrived, setArrivedState] = useState<Record<number, Arrived>>({});
  // The counts as the last scan left them, so scans that come faster than renders each see the one before.
  const arrivedRef = useRef<Record<number, Arrived>>({});
  const setArrived = (change: (current: Record<number, Arrived>) => Record<number, Arrived>) => {
    arrivedRef.current = change(arrivedRef.current);
    setArrivedState(arrivedRef.current);
  };
  // After an answer that never came (no HTTP status, or a 5xx) the receive may have landed: the next try is sent
  // as it is, for the server to replay, rather than judged against an outstanding that already took it.
  const [maybeLanded, setMaybeLanded] = useState(false);
  const leaving = useRef(false);
  const [destinations, setDestinations] = useState<Record<number, number | null>>({});
  const [marks, setMarks] = useState<ReadonlyMap<number, string>>(new Map());
  const [problem, setProblem] = useState<string>();
  const [scan, setScan] = useState("");
  const [scanError, setScanError] = useState<string>();
  const [closing, setClosing] = useState(false);
  const scanRef = useRef<HTMLInputElement>(null);

  // Leaving with counts not yet received asks first — a Back, a breadcrumb, another transfer's note scanned —
  // and closing the tab warns.
  const counted = () =>
    !leaving.current && transfer.lines.some((line) => arrivedTotal(line, arrivedRef.current[line.id] ?? none) > 0);
  useBlocker({
    shouldBlockFn: async () =>
      counted()
        ? !(await askToLeave({
            title: t("leaveReceiveTitle"),
            body: t("leaveReceiveBody"),
            leave: t("leaveReceive"),
            stay: t("keepReceiving"),
          }))
        : false,
    enableBeforeUnload: counted,
  });

  // The lines still in transit; a refusal reloads the transfer and the counts stay on their lines.
  const rows = transfer.lines.filter((line) => line.inTransit > 0);
  const countOf = (line: InventoryTransferLine) => arrived[line.id] ?? none;
  const destinationOf = (line: InventoryTransferLine) =>
    destinations[line.id] !== undefined ? destinations[line.id] : (rootLocationId ?? null);
  const remaining = rows
    .map((line) => ({ line, rest: roundQuantity(line.inTransit - arrivedTotal(line, countOf(line))) }))
    .filter((one) => one.rest > 0);
  const anything = rows.some((line) => arrivedTotal(line, countOf(line)) > 0);

  const edit = (lineId: number, change: Partial<Arrived>) =>
    setArrived((current) => ({ ...current, [lineId]: { ...(current[lineId] ?? none), ...change } }));

  const [linesBody, setLinesBody] = useState<HTMLTableSectionElement | null>(null);
  const virtualizer = useWindowVirtualizer({
    count: rows.length,
    estimateSize: () => LINE_HEIGHT_ESTIMATE,
    overscan: LINES_OVERSCAN,
    scrollMargin: linesBody ? linesBody.getBoundingClientRect().top + window.scrollY : 0,
    getItemKey: (index) => rows[index]?.id ?? index,
    measureElement: (element) => element.getBoundingClientRect().height || LINE_HEIGHT_ESTIMATE,
  });
  const drawn = virtualizer.getVirtualItems();
  const margin = virtualizer.options.scrollMargin;
  const spaceAbove = drawn.length > 0 ? drawn[0].start - margin : 0;
  const spaceBelow = drawn.length > 0 ? virtualizer.getTotalSize() - (drawn[drawn.length - 1].end - margin) : 0;

  // Each scan of an item's SKU or barcode counts one more of it, a box's barcode one box; the note's TRF token opens its transfer.
  const scanned = async (code: string) => {
    if (!code) return;
    setScan("");
    setScanError(undefined);
    const keep = () => setScan((current) => current || code);
    try {
      const matches = await queryClient.fetchQuery(lookupQueryOptions(code));
      const other = matches.find((match) => match.kind === "transfer")?.transfer;
      if (other) {
        if (other.id !== transfer.id) void navigate(receiveTransferLinkOptions(other.id));
        return;
      }
      const variant = matches.find((match) => match.kind === "variant")?.variant;
      const candidates = variant ? rows.filter((line) => line.variant.id === variant.id) : [];
      if (!variant || candidates.length === 0) {
        keep();
        setScanError(variant ? t("scanNotOnTransfer", { code }) : t("scanNoVariant", { code }));
        return;
      }
      // The first of the variant's lines with room left takes it, judged on the counts as the last scan left
      // them; when all are full, the first.
      const line =
        candidates.find((one) => arrivedTotal(one, arrivedRef.current[one.id] ?? none) < one.inTransit) ??
        candidates[0];
      const packaging = line.variant.packagings?.find((one) => one.barcode === code);
      setArrived((current) => {
        const now = current[line.id] ?? none;
        return {
          ...current,
          [line.id]: packaging
            ? { ...now, packs: { ...now.packs, [packaging.id]: (now.packs[packaging.id] ?? 0) + 1 } }
            : { ...now, quantity: roundQuantity(Number(now.quantity || 0) + 1) },
        };
      });
      const index = rows.indexOf(line);
      if (index >= 0) virtualizer.scrollToIndex(index, { align: "center" });
    } catch (error) {
      keep();
      setScanError(refusalMessage(t, error as Error));
    } finally {
      scanRef.current?.focus();
    }
  };

  const receiving = useMutation({
    mutationFn: ({
      lines,
      close,
    }: {
      lines: InventoryTransferReceiveLine[];
      close?: { reason: InventoryTransferCloseReason; note?: string };
    }) => receiveTransfer(transfer.id, { clientRequestId, lines, ...(close ? { close } : {}) }),
    onSuccess: async (answer) => {
      setClosing(false);
      setMaybeLanded(false);
      leaving.current = true;
      notifications.show({
        color: "teal",
        title: answer.status === "received" ? t("transferReceived") : t("transferPartlyReceived"),
        message: t("transferNumber", { number: answer.number }),
      });
      await invalidate();
      void navigate(transferLinkOptions(answer.id));
    },
    onError: async (error, { lines }) => {
      setClosing(false);
      const status = (error as { status?: unknown }).status;
      if (typeof status !== "number" || status >= 500) {
        setMaybeLanded(true);
        setProblem(t("receiveMaybeLanded"));
        await invalidate();
        return;
      }
      setMaybeLanded(false);
      if (refusalCode(error) === "client_request_reused") {
        // An earlier receive from this page landed and its answer was lost: what it took is no longer in transit.
        setClientRequestId(crypto.randomUUID());
        setProblem(t("receiveReused"));
        await invalidate();
        return;
      }
      const failing = refusedLines(error);
      const next = new Map<number, string>();
      for (const line of failing) {
        const sent = lines[line.index];
        if (!sent) continue;
        next.set(
          sent.lineId,
          (refusalCodes as readonly string[]).includes(line.code) ? t(`refusal.${line.code}`) : line.detail,
        );
      }
      setMarks(next);
      setProblem(failing.length > 0 ? t("linesRefused", { count: failing.length }) : refusalMessage(t, error));
      // Someone else may have received part of it meanwhile (I41): the outstanding is read again, the counts kept.
      await invalidate();
    },
  });

  const submit = (close?: { reason: InventoryTransferCloseReason; note?: string }) => {
    const gaps = new Map<number, string>();
    const lines: InventoryTransferReceiveLine[] = [];
    // Every line counted, even one an earlier try may already have taken out of transit: that try is replayed whole.
    for (const line of transfer.lines) {
      const count = countOf(line);
      const total = arrivedTotal(line, count);
      if (total <= 0) continue;
      const toLocationId = destinationOf(line);
      if (total > line.inTransit && !maybeLanded) gaps.set(line.id, t("refusal.transfer_over_received"));
      else if (toLocationId === null) gaps.set(line.id, t("lineGap_location"));
      lines.push({
        lineId: line.id,
        toLocationId: toLocationId ?? 0,
        ...(isPacked(count)
          ? { entered: enteredOf(count.packs, count.quantity) }
          : { quantity: roundQuantity(Number(count.quantity)) }),
      });
    }
    setMarks(gaps);
    if (gaps.size > 0) {
      setClosing(false);
      setProblem(t("linesIncomplete"));
      return;
    }
    if (lines.length === 0 && !close) {
      setProblem(t("refusal.nothing_to_post"));
      return;
    }
    setProblem(undefined);
    receiving.mutate({ lines, close });
  };

  const acceptAll = () =>
    setArrived(() => Object.fromEntries(rows.map((line) => [line.id, { quantity: line.inTransit, packs: {} }])));

  return (
    <Stack gap="md">
      <PageSection>
        <Stack gap="sm">
          <Group align="flex-end" gap="sm">
            <TextInput
              ref={scanRef}
              autoFocus
              style={{ flex: 1 }}
              data-assist="inventory.transfer-receive.scan"
              label={t("receiveScanLabel")}
              description={t("receiveScanHint")}
              leftSection={<IconScan size={16} />}
              value={scan}
              error={scanError}
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
            <Button variant="light" leftSection={<IconChecks size={16} />} onClick={acceptAll}>
              {t("acceptAll")}
            </Button>
          </Group>
          <Table.ScrollContainer minWidth={760}>
            <Table verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("columnItem")}</Table.Th>
                  <Table.Th ta="right">{t("columnSent")}</Table.Th>
                  <Table.Th ta="right">{t("columnReceivedBefore")}</Table.Th>
                  <Table.Th>{t("columnNow")}</Table.Th>
                  <Table.Th>{t("receiveTo", { site: transfer.toSite.code })}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody ref={setLinesBody}>
                {spaceAbove > 0 && (
                  <tr>
                    <td colSpan={5} style={{ height: spaceAbove, padding: 0, border: 0 }} />
                  </tr>
                )}
                {drawn.map((item) => {
                  const line = rows[item.index];
                  const count = countOf(line);
                  const packagings = line.variant.packagings ?? [];
                  const label = `${line.variant.sku} ${line.lineNo}`;
                  const mark = marks.get(line.id);
                  const total = arrivedTotal(line, count);
                  return (
                    <Table.Tr
                      key={line.id}
                      ref={virtualizer.measureElement}
                      data-index={item.index}
                      data-refused={mark ? "true" : undefined}
                      bg={mark ? "var(--mantine-color-red-light)" : undefined}
                    >
                      <Table.Td miw={220}>
                        <Text size="sm">
                          {line.variant.sku} · {line.variant.productName}
                        </Text>
                        {mark && (
                          <Text size="xs" c="red" fw={500}>
                            {mark}
                          </Text>
                        )}
                      </Table.Td>
                      <Table.Td ta="right">{quantity(line.sent, line.variant.unit)}</Table.Td>
                      <Table.Td ta="right">{quantity(line.received + line.missing, line.variant.unit)}</Table.Td>
                      <Table.Td w={packagings.length > 0 ? 200 : 150}>
                        <Stack gap={4}>
                          {packagings.length > 0 && (
                            <PackagingSteppers
                              packagings={packagings}
                              unit={line.variant.unit}
                              counts={count.packs}
                              onChange={(packs) => edit(line.id, { packs })}
                              label={(packaging) => t("nowPackagingOf", { name: packaging.name, line: label })}
                            />
                          )}
                          <NumberInput
                            size={packagings.length > 0 ? "xs" : undefined}
                            aria-label={t("nowOf", { line: label })}
                            decimalScale={QUANTITY_DECIMALS}
                            allowNegative={false}
                            min={0}
                            rightSection={
                              <Text size="xs" c="dimmed" pr="xs">
                                {line.variant.unit}
                              </Text>
                            }
                            rightSectionWidth={48}
                            value={count.quantity}
                            onChange={(value) =>
                              edit(line.id, { quantity: value === "" ? "" : roundQuantity(Number(value)) })
                            }
                          />
                          {isPacked(count) && (
                            <Text size="xs" c="dimmed">
                              = {quantity(total, line.variant.unit)}
                            </Text>
                          )}
                        </Stack>
                      </Table.Td>
                      <Table.Td miw={200}>
                        <LocationPicker
                          compact
                          required
                          siteId={transfer.toSite.id}
                          label={t("destinationOf", { line: label })}
                          value={destinationOf(line)}
                          onChange={(locationId) =>
                            setDestinations((current) => ({ ...current, [line.id]: locationId }))
                          }
                        />
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
                {spaceBelow > 0 && (
                  <tr>
                    <td colSpan={5} style={{ height: spaceBelow, padding: 0, border: 0 }} />
                  </tr>
                )}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
          {remaining.length > 0 && (
            <Text size="sm" c="dimmed">
              {t("stillInTransit", { count: remaining.length })}
            </Text>
          )}
        </Stack>
      </PageSection>
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotReceive")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button variant="default" renderRoot={(props) => <Link {...transferLinkOptions(transfer.id)} {...props} />}>
          {t("back")}
        </Button>
        <Button onClick={() => submit()} loading={receiving.isPending && !closing} disabled={!anything}>
          {t("receiveTransfer")}
        </Button>
        <Button variant="light" onClick={() => setClosing(true)} disabled={remaining.length === 0}>
          {t("receiveAndCloseEllipsis")}
        </Button>
      </Group>
      {closing && (
        <CloseDifferenceDialog
          opened
          onClose={() => setClosing(false)}
          transferNumber={transfer.number}
          loading={receiving.isPending}
          differences={remaining.map(({ line, rest }) => ({
            key: String(line.id),
            label: `${line.variant.sku} · ${line.variant.productName}`,
            quantity: rest,
            unit: line.variant.unit,
          }))}
          onConfirm={(close) => submit(close)}
        />
      )}
    </Stack>
  );
};

/**
 * Receiving a transfer (phase 2 design §7, I40–I42): a scan field — each scan
 * of an item counts one more on its line, the følgeseddel's `TRF-…` opens its
 * transfer — Accept all, and per line what arrived now and where it goes at
 * the to site, its root by default. Receive posts what was counted, and the
 * rest stays in transit; Receive and close the difference… records the rest
 * as missing with a reason. Only the lines near the window are drawn, as a
 * long transfer may hold hundreds. Needs `inventory:transfers-manage` (I63).
 */
export const ReceiveTransferPage = () => {
  const { t, stamp } = useInventoryFormat();
  const { transferId: rawId } = useParams({ strict: false }) as { transferId: string };
  const transferId = Number(rawId);
  const meta = useQuery(inventoryMetaQueryOptions());
  const query = useQuery(transferQueryOptions(transferId));
  const data = query.data;
  const toSite = useQuery({ ...siteQueryOptions(data?.toSite.id ?? 0), enabled: !!data });
  const title = data ? t("receiveTitle", { number: data.number, site: data.fromSite.code }) : t("receiveTransfer");
  const failed = meta.error ?? query.error ?? toSite.error;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH },
          { label: t("navTransfers"), to: TRANSFERS_ROUTE_PATH },
          {
            label: data ? t("transferNumber", { number: data.number }) : t("transferById", { id: transferId }),
            to: `${TRANSFERS_ROUTE_PATH}/${transferId}`,
          },
          { label: t("receiveTransfer") },
        ]}
        title={title}
        description={data?.sentAt && t("sentByAt", { who: data.sentBy?.displayName ?? "—", when: stamp(data.sentAt) })}
      />
      {failed ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {failed.message}
        </Alert>
      ) : meta.isPending || !data || toSite.isPending ? (
        <ContentSkeleton rows={5} />
      ) : !canTransfer(meta.data) ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_transfer")}
        </Alert>
      ) : !isReceivable(data) ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          <Stack gap="xs">
            <Text size="sm">{t("refusal.transfer_not_receivable")}</Text>
            <Anchor size="sm" renderRoot={(props) => <Link {...transferLinkOptions(data.id)} {...props} />}>
              {t("openTransfer", { number: data.number })}
            </Anchor>
          </Stack>
        </Alert>
      ) : (
        <ReceiveLines key={data.id} transfer={data} rootLocationId={toSite.data?.rootLocationId} />
      )}
    </Stack>
  );
};
