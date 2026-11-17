import { Alert, Anchor, Button, Card, Group, Select, Stack, Table, Text, TextInput, Title } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import {
  invoicePanelQueryOptions,
  matchAllPurchasingInvoice,
  matchPurchasingInvoice,
  type PurchasingInvoiceMatchLineInput,
  type PurchasingInvoicePanel,
  purchaseOrderQueryOptions,
  purchaseOrdersQueryOptions,
  unmatchPurchasingInvoice,
} from "../api/purchasing";
import { PURCHASING_QUERY_KEY, refusalCode } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { usePurchasingFormat } from "../lib/format";
import { purchaseOrderLinkOptions } from "../lib/routes";
import { ConfirmDialog } from "./common";
import { MatchState, MatchWarnings } from "./order-invoices";

export interface PurchaseOrderPanelProps {
  /** Accounting's supplier invoice the panel sits on. */
  invoiceId: number;
  /** `purchasing:invoices-match`: allocate, Match all received and unmatch (P37). */
  canMatch: boolean;
}

/**
 * The **Purchase order** panel on Accounting's supplier-invoice page
 * (purchasing design P36–P38, A20), which the host composes: the orders the
 * invoice's reference names, its matches with their differences, and — for
 * a holder of purchasing:invoices-match — the allocation of its lines to an
 * order's lines, Match all received and Unmatch. The invoice's lines show
 * their text and net only when the caller holds accounting:read; others see
 * the positions and the matched quantities alone.
 */
export const PurchaseOrderPanel = ({ invoiceId, canMatch }: PurchaseOrderPanelProps) => {
  const { t } = usePurchasingFormat();
  const panel = useQuery(invoicePanelQueryOptions(invoiceId));
  return (
    <Card withBorder padding="md" data-testid="purchase-order-panel">
      <Stack gap="sm">
        <Title order={4}>{t("purchaseOrderPanel")}</Title>
        {panel.isPending ? (
          <Text size="sm" c="dimmed">
            {t("loading")}
          </Text>
        ) : panel.isError ? (
          <Text size="sm" c="red">
            {refusalMessage(panel.error, t)}
          </Text>
        ) : (
          <PanelBody panel={panel.data} canMatch={canMatch && panel.data.state !== "reversed"} />
        )}
      </Stack>
    </Card>
  );
};

const PanelBody = ({ panel, canMatch }: { panel: PurchasingInvoicePanel; canMatch: boolean }) => {
  const { t, quantity, money } = usePurchasingFormat();
  const queryClient = useQueryClient();
  const [orderId, setOrderId] = useState<number | null>(null);
  const credit = panel.kind === "credit_note";
  const refresh = async (next: PurchasingInvoicePanel, message: string) => {
    queryClient.setQueryData(invoicePanelQueryOptions(panel.invoiceId).queryKey, next);
    await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
    notifications.show({ color: "green", message });
  };
  const failed = (error: unknown) => notifications.show({ color: "red", message: refusalMessage(error, t) });
  const matchAll = useMutation({
    mutationFn: (id: number) => matchAllPurchasingInvoice(panel.invoiceId, id),
    onSuccess: (next) => refresh(next, t("matchedAll")),
    onError: failed,
  });
  const unmatch = useMutation({
    mutationFn: (input: { orderId?: number; linePosition?: number }) =>
      unmatchPurchasingInvoice(panel.invoiceId, input),
    onSuccess: (next) => refresh(next, t("unmatched")),
    onError: failed,
  });
  const textAt = (position: number) => panel.lines.find((l) => l.position === position)?.text ?? null;
  const orders = [...new Map(panel.matches.map((m) => [m.orderId, m.orderNumber ?? m.orderId])).entries()];
  return (
    <Stack gap="sm">
      {panel.state === "reversed" && (
        <Alert color="gray" variant="light">
          {t("invoiceReversedNote")}
        </Alert>
      )}
      {panel.differences > 0 && (
        <Alert color="orange" variant="light" data-testid="panel-differences">
          {t("matchDifferences", { count: panel.differences })}
        </Alert>
      )}
      {panel.matches.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("notMatched")}
        </Text>
      ) : (
        <Table data-testid="panel-matches">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("invoiceLine")}</Table.Th>
              <Table.Th>{t("order")}</Table.Th>
              <Table.Th>{t("product")}</Table.Th>
              <Table.Th ta="right">{t("matchedQuantity")}</Table.Th>
              {panel.linesVisible && <Table.Th ta="right">{t("invoiceLineNet")}</Table.Th>}
              <Table.Th />
              <Table.Th />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {panel.matches.map((m) => (
              <Table.Tr key={m.id}>
                <Table.Td>
                  {panel.linesVisible && textAt(m.linePosition)
                    ? `${m.linePosition}. ${textAt(m.linePosition)}`
                    : m.linePosition}
                </Table.Td>
                <Table.Td>
                  <Anchor renderRoot={(props) => <Link {...purchaseOrderLinkOptions(m.orderId)} {...props} />}>
                    {m.orderNumber ?? m.orderId}
                  </Anchor>
                </Table.Td>
                <Table.Td>{m.name}</Table.Td>
                <Table.Td ta="right">{`${quantity(m.quantity)} ${m.unit}`}</Table.Td>
                {panel.linesVisible && <Table.Td ta="right">{m.lineNet ? money(m.lineNet, m.currency) : ""}</Table.Td>}
                <Table.Td>
                  <Group gap={4}>
                    <MatchState state={m.state} />
                    <MatchWarnings match={m} />
                  </Group>
                </Table.Td>
                <Table.Td>
                  {canMatch && (
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="red"
                      loading={unmatch.isPending}
                      onClick={() => unmatch.mutate({ linePosition: m.linePosition })}
                    >
                      {t("unmatch")}
                    </Button>
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      {canMatch && orders.length > 1 && (
        <Group gap="xs">
          {orders.map(([id, number]) => (
            <Button key={id} size="compact-xs" variant="default" onClick={() => unmatch.mutate({ orderId: id })}>
              {t("unmatchOrder", { number })}
            </Button>
          ))}
        </Group>
      )}
      {panel.suggestions.length > 0 && (
        <Stack gap={4} data-testid="panel-suggestions">
          <Text size="sm" fw={600}>
            {t("suggestedOrders")}
          </Text>
          {panel.suggestions.map((s) => (
            <Group key={s.orderId} gap="xs">
              <Anchor size="sm" renderRoot={(props) => <Link {...purchaseOrderLinkOptions(s.orderId)} {...props} />}>
                {t("orderTitle", { number: s.number })}
              </Anchor>
              <Text size="sm" c="dimmed">
                {`${money(s.netTotal, s.currency)} · ${t("linesToInvoice", { count: s.toInvoice })}`}
              </Text>
              {canMatch && !credit && (
                <Button
                  size="compact-xs"
                  loading={matchAll.isPending}
                  disabled={s.toInvoice === 0}
                  onClick={() => matchAll.mutate(s.orderId)}
                >
                  {t("matchAllReceived")}
                </Button>
              )}
              {canMatch && (
                <Button size="compact-xs" variant="default" onClick={() => setOrderId(s.orderId)}>
                  {t("allocate")}
                </Button>
              )}
            </Group>
          ))}
        </Stack>
      )}
      {canMatch && <OrderSearch supplierId={panel.supplierId} value={orderId} onChange={setOrderId} />}
      {canMatch && orderId !== null && (
        <Allocation
          panel={panel}
          orderId={orderId}
          onMatchAll={credit ? undefined : () => matchAll.mutate(orderId)}
          onDone={(next) => {
            setOrderId(null);
            return refresh(next, t("matched"));
          }}
        />
      )}
    </Stack>
  );
};

/** Another order of the invoice's supplier, by number or the supplier's reference. */
const OrderSearch = ({
  supplierId,
  value,
  onChange,
}: {
  supplierId: number;
  value: number | null;
  onChange: (id: number | null) => void;
}) => {
  const { t } = usePurchasingFormat();
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const list = useQuery(
    purchaseOrdersQueryOptions({
      supplierId,
      status: ["sent", "partially_received", "received", "closed"],
      q: debounced.trim() || undefined,
      pageSize: 20,
    }),
  );
  const options = (list.data?.items ?? [])
    .filter((o) => o.number !== null && o.number !== undefined)
    .map((o) => ({ value: String(o.id), label: t("orderTitle", { number: o.number }) }));
  return (
    <Select
      label={t("pickOrder")}
      placeholder={t("pickOrderHint")}
      searchable
      clearable
      data={options}
      value={value === null ? null : String(value)}
      onSearchChange={setSearch}
      filter={({ options: all }) => all}
      nothingFoundMessage={t("noOrdersFound")}
      onChange={(next) => onChange(next === null ? null : Number(next))}
    />
  );
};

/**
 * The allocation of the invoice's unmatched lines to one order's lines: per
 * invoice line, the order line it bills and the quantity in that line's
 * unit. More than received and not invoiced is asked about first, and sent
 * again with acceptOver (P37).
 */
const Allocation = ({
  panel,
  orderId,
  onMatchAll,
  onDone,
}: {
  panel: PurchasingInvoicePanel;
  orderId: number;
  onMatchAll?: () => void;
  onDone: (next: PurchasingInvoicePanel) => Promise<void>;
}) => {
  const { t, money, quantity } = usePurchasingFormat();
  const order = useQuery(purchaseOrderQueryOptions(orderId));
  const matched = new Set(panel.matches.map((m) => m.linePosition));
  const free = panel.lines.filter((l) => !matched.has(l.position));
  const [rows, setRows] = useState<Record<number, { orderLineId: string | null; quantity: string }>>({});
  const [asking, setAsking] = useState<PurchasingInvoiceMatchLineInput[] | null>(null);
  const match = useMutation({
    mutationFn: ({ lines, acceptOver }: { lines: PurchasingInvoiceMatchLineInput[]; acceptOver: boolean }) =>
      matchPurchasingInvoice(panel.invoiceId, { orderId, lines, acceptOver }),
    onSuccess: async (next) => {
      setAsking(null);
      await onDone(next);
    },
    onError: (error, variables) => {
      if (refusalCode(error) === "over_invoiced" && !variables.acceptOver) {
        setAsking(variables.lines);
        return;
      }
      notifications.show({ color: "red", message: refusalMessage(error, t) });
    },
  });
  if (!order.data) return null;
  const options = order.data.lines.map((l) => ({
    value: String(l.id),
    label: `${l.position}. ${l.kind === "text" ? l.description : l.name} (${quantity(l.received)} / ${quantity(l.quantity)} ${l.unit})`,
  }));
  const lines: PurchasingInvoiceMatchLineInput[] = free.flatMap((l) => {
    const row = rows[l.position];
    return row?.orderLineId && row.quantity.trim()
      ? [{ linePosition: l.position, orderLineId: Number(row.orderLineId), quantity: row.quantity.trim() }]
      : [];
  });
  const set = (position: number, change: Partial<{ orderLineId: string | null; quantity: string }>) =>
    setRows((current) => ({
      ...current,
      [position]: { ...(current[position] ?? { orderLineId: null, quantity: "" }), ...change },
    }));
  return (
    <Stack gap="xs" data-testid="panel-allocation">
      <Group justify="space-between">
        <Text size="sm" fw={600}>
          {t("allocateTo", { number: order.data.number ?? order.data.id })}
        </Text>
        {onMatchAll && (
          <Button size="compact-xs" variant="default" onClick={onMatchAll}>
            {t("matchAllReceived")}
          </Button>
        )}
      </Group>
      {free.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("everyLineMatched")}
        </Text>
      ) : (
        free.map((l) => (
          <Group key={l.position} align="flex-end" wrap="nowrap" data-testid="allocation-row">
            <Text size="sm" w={220}>
              {panel.linesVisible && l.text
                ? `${l.position}. ${l.text}${l.net ? ` · ${money(l.net, panel.currency)}` : ""}`
                : t("invoiceLineNumber", { position: l.position })}
            </Text>
            <Select
              aria-label={t("orderLineFor", { position: l.position })}
              placeholder={t("pickOrderLine")}
              data={options}
              value={rows[l.position]?.orderLineId ?? null}
              onChange={(value) => set(l.position, { orderLineId: value })}
              w={320}
            />
            <TextInput
              aria-label={t("quantityFor", { position: l.position })}
              value={rows[l.position]?.quantity ?? ""}
              onChange={(e) => set(l.position, { quantity: e.currentTarget.value })}
              w={120}
            />
          </Group>
        ))
      )}
      <Group justify="flex-end">
        <Button
          loading={match.isPending}
          disabled={lines.length === 0}
          onClick={() => match.mutate({ lines, acceptOver: false })}
        >
          {t("match")}
        </Button>
      </Group>
      {asking && (
        <ConfirmDialog
          title={t("overInvoicedTitle")}
          body={t("overInvoicedBody")}
          confirm={t("matchAnyway")}
          loading={match.isPending}
          onClose={() => setAsking(null)}
          onConfirm={() => match.mutate({ lines: asking, acceptOver: true })}
        />
      )}
    </Stack>
  );
};
