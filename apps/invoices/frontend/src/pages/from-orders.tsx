import {
  Alert,
  Badge,
  Button,
  Checkbox,
  CloseButton,
  Group,
  NumberInput,
  SegmentedControl,
  Stack,
  Stepper,
  Table,
  Text,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type DueOrder,
  type DueOrderLine,
  dueOrdersQueryOptions,
  type FromOrdersDraft,
  postFromOrders,
} from "../api/from-orders";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import { CustomerPicker } from "../components/customer-picker";
import { DocumentLink, RouteLink } from "../components/document-link";
import { invoicesCatalog } from "../i18n";
import { refusalMessage, refusalProblem } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { invoiceLinkOptions } from "../lib/routes";

export interface FromOrdersPageProps {
  /** Whether the caller may make drafts (`invoices:create`), which the wizard's API asks. The host reads it. */
  canCreate?: boolean;
  /** Whether the caller holds `customers:view`, which the customer picker needs. */
  canViewCustomers: boolean;
  /** An order to start with, chosen — an order's *Invoice* button (`?orderId=`). */
  initialOrderId?: number;
  /** A customer to start with (`?customerId=`). */
  initialCustomerId?: number;
}

type Mode = "perOrder" | "combined";

/** The fields a combined invoice's orders must agree on, as the server names a separation. */
const GROUP_FIELDS = ["currency", "your_reference", "project", "payment_terms", "delivery_address"] as const;

/** docmath's line net: gross and allowance each rounded to øre, half away from zero. */
const round2 = (v: number) => (Math.sign(v) * Math.round(Math.abs(v) * 100 + Number.EPSILON)) / 100;
const lineNet = (quantity: number, price: number, discount: number) => {
  const gross = round2(quantity * price);
  return round2(gross - round2((gross * discount) / 100));
};

/** A delivery address as the server compares it: spaces collapsed, case folded, the country upper case; "" for none. */
const addressKey = (a: DueOrder["deliveryAddress"]) => {
  if (!a) return "";
  const norm = (s?: string) => (s ?? "").trim().split(/\s+/).join(" ").toLowerCase();
  return [norm(a.line1), norm(a.line2), norm(a.postalCode), norm(a.city), a.country.trim().toUpperCase()].join(
    "\u001f",
  );
};

/** What a combined invoice's orders share (orders design O39). */
const groupKey = (o: DueOrder) => ({
  currency: o.currency,
  your_reference: o.yourReference.trim(),
  project: o.projectId ?? 0,
  payment_terms: o.paymentTermsDays ?? -1,
  delivery_address: addressKey(o.deliveryAddress),
});

/**
 * A document's key: an order and a subscription are numbered apart (orders
 * design O43), so a document is its kind and its id together.
 */
const keyOf = (o: DueOrder) => `${o.kind}:${o.id}`;

/** Whether a document is a subscription, whose lines are its due periods' and are invoiced whole. */
const isSubscription = (o: DueOrder) => o.kind === "subscription";

/** Today as the calendar names it, in the reader's zone: the date a subscription's periods are invoiced through. */
const todayText = () => {
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
};

/** One invoice the wizard would make: a customer's orders, and what kept it apart from the customer's others. */
interface Preview {
  customerId: number;
  customerName: string;
  orders: DueOrder[];
  separatedBy: string[];
}

/** The server's grouping (orders design O39), for the preview: by customer, then by what the orders share. */
const previewOf = (orders: DueOrder[], mode: Mode): Preview[] => {
  const sorted = [...orders].sort(
    (a, b) => a.customerId - b.customerId || a.kind.localeCompare(b.kind) || a.number - b.number,
  );
  const groups: (Preview & { key: ReturnType<typeof groupKey> })[] = [];
  const same = (a: ReturnType<typeof groupKey>, b: ReturnType<typeof groupKey>) =>
    GROUP_FIELDS.every((f) => a[f] === b[f]);
  for (const o of sorted) {
    const key = groupKey(o);
    const into =
      mode === "combined" ? groups.find((g) => g.customerId === o.customerId && same(g.key, key)) : undefined;
    if (into) into.orders.push(o);
    else
      groups.push({ customerId: o.customerId, customerName: o.customerName ?? "", orders: [o], separatedBy: [], key });
  }
  if (mode === "combined") {
    for (const g of groups) {
      for (const other of groups) {
        if (other === g || other.customerId !== g.customerId) continue;
        for (const f of GROUP_FIELDS)
          if (g.key[f] !== other.key[f] && !g.separatedBy.includes(f)) g.separatedBy.push(f);
      }
    }
  }
  return groups;
};

/**
 * Invoices' *New invoice → From orders* (orders design O39): step 1 picks the
 * customers — or starts from the order named in the URL — and lists their
 * orders with lines left to invoice, a held or draft order shown with why it
 * cannot be invoiced; step 2 shows the chosen orders' lines with the quantity
 * to invoice, editable down for part invoicing, and what remains after;
 * step 3 chooses one invoice per order or combined per customer, previewing
 * the invoices and why a customer's orders stay apart. "Create" makes the
 * drafts and opens the one made, or lists them when there are several.
 */
export const FromOrdersPage = ({
  canCreate = true,
  canViewCustomers,
  initialOrderId,
  initialCustomerId,
}: FromOrdersPageProps) => {
  const { t, money, unitPrice, number, date, month } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const [step, setStep] = useState(0);
  const [customers, setCustomers] = useState<number[]>(initialCustomerId ? [initialCustomerId] : []);
  const [picker, setPicker] = useState<number | null>(null);
  const orderIds = customers.length === 0 && initialOrderId ? [initialOrderId] : [];
  const query = customers.length > 0 ? { customerIds: customers } : { orderIds };
  const due = useQuery({
    ...dueOrdersQueryOptions(query),
    enabled: canCreate && (customers.length > 0 || orderIds.length > 0),
    retry: false,
  });
  // The orders unticked, and the quantities changed: everything invoiceable
  // starts chosen, in full.
  const [unchecked, setUnchecked] = useState<Set<string>>(new Set());
  // As typed: a number, or the text of one being typed.
  const [quantities, setQuantities] = useState<Record<number, number | string>>({});
  const [mode, setMode] = useState<Mode>("perOrder");
  const [refusal, setRefusal] = useState<string[] | null>(null);
  const [made, setMade] = useState<FromOrdersDraft[] | null>(null);

  const orders = due.data?.orders ?? [];
  const chosenOrders = orders.filter((o) => !o.blocked && !unchecked.has(keyOf(o)));
  const quantityOf = (l: DueOrderLine) => {
    // A subscription's period is invoiced whole (orders design O43).
    if (l.periodStart) return l.toInvoice;
    const typed = quantities[l.lineId];
    if (typed === undefined) return l.toInvoice;
    const n = Number(typed);
    return typed === "" || !Number.isFinite(n) ? 0 : n;
  };
  const chosenLines = chosenOrders.flatMap((o) =>
    o.lines.filter((l) => quantityOf(l) > 0).map((l) => ({ order: o, line: l })),
  );
  const chosenOrderLines = chosenLines.filter(({ order }) => !isSubscription(order));
  const chosenSubscriptions = chosenOrders.filter(isSubscription);
  /** A period as the wizard names it: a whole month by its name, else its first and last day. */
  const periodLabel = (l: DueOrderLine) => {
    if (!l.periodStart || !l.periodEnd) return "";
    const start = l.periodStart;
    const last = new Date(`${start.slice(0, 7)}-01T00:00:00Z`);
    last.setUTCMonth(last.getUTCMonth() + 1, 0);
    return start.endsWith("-01") && l.periodEnd === last.toISOString().slice(0, 10)
      ? month(start)
      : `${date(start)} – ${date(l.periodEnd)}`;
  };
  /** A document as the wizard names it: "Order 2210", or "Subscription 3001 – January 2027" with its periods. */
  const documentLabel = (o: DueOrder, withPeriods = false) => {
    if (!isSubscription(o)) return t("fromOrders.order", { number: o.number });
    const periods = [...new Set(o.lines.map(periodLabel))];
    const label = t("fromOrders.subscription", { number: o.number });
    return withPeriods && periods.length > 0 ? `${label} – ${periods.join(", ")}` : label;
  };
  const invoiced = chosenOrders.filter((o) => o.lines.some((l) => quantityOf(l) > 0));
  const amountOf = (l: DueOrderLine) => lineNet(quantityOf(l), l.unitPrice, l.discountPercent);
  const chosenAmount = round2(chosenLines.reduce((sum, { line }) => sum + amountOf(line), 0));
  const preview = previewOf(invoiced, mode);
  const lineLabel = (o: DueOrder, l: DueOrderLine) =>
    t("fromOrders.refusedLine", { number: o.number, position: l.position, description: l.description });

  const create = useMutation({
    mutationFn: () =>
      postFromOrders({
        combine: mode === "combined",
        lines: chosenOrderLines.map(({ order, line }) => ({
          orderId: order.id,
          lineId: line.lineId,
          quantity: quantityOf(line),
        })),
        ...(chosenSubscriptions.length > 0
          ? { subscriptions: chosenSubscriptions.map((o) => ({ subscriptionId: o.id, through: todayText() })) }
          : {}),
      }),
    onMutate: () => setRefusal(null),
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("fromOrdersCreated", { count: result.invoices.length }) });
      if (result.invoices.length === 1) {
        navigate(invoiceLinkOptions(result.invoices[0].invoiceId));
        return;
      }
      setMade(result.invoices);
    },
    onError: (error) => {
      // A line refused names the order line it is about.
      const lineId = refusalProblem(error).orderLineId;
      const at = chosenLines.find(({ line }) => line.lineId === lineId);
      const words =
        error instanceof ApiValidationError
          ? Object.values(error.errors).flat()
          : [refusalMessage(error, t, date), ...(at ? [lineLabel(at.order, at.line)] : [])];
      setRefusal(words);
      // What is left to invoice may have moved: list it again.
      void queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "from-orders"] });
    },
  });

  const toggle = (id: string, on: boolean) =>
    setUnchecked((current) => {
      const next = new Set(current);
      if (on) next.delete(id);
      else next.add(id);
      return next;
    });

  if (!canCreate) {
    return (
      <Stack gap="lg">
        <PageHeader title={t("fromOrders.title")} />
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("fromOrders.notAllowed")}
        </Alert>
      </Stack>
    );
  }

  if (made) {
    return (
      <Stack gap="lg">
        <PageHeader title={t("fromOrders.title")} description={t("fromOrders.createdTitle")} />
        <Stack gap="xs" data-testid="from-orders-made">
          {made.map((d) => (
            <Text key={d.invoiceId} size="sm">
              <DocumentLink invoiceId={d.invoiceId}>
                {t("fromOrders.openDraft", {
                  id: d.invoiceId,
                  orders: [
                    ...d.orderNumbers.map(String),
                    ...(d.subscriptionNumbers ?? []).map((n) => t("fromOrders.subscriptionShort", { number: n })),
                  ].join(", "),
                })}
              </DocumentLink>
            </Text>
          ))}
        </Stack>
        <Group>
          <RouteLink href="/invoices" to={{ to: "/invoices" }}>
            {t("fromOrders.backToInvoices")}
          </RouteLink>
        </Group>
      </Stack>
    );
  }

  return (
    <Stack gap="lg">
      <PageHeader title={t("fromOrders.title")} description={t("fromOrders.description")} />
      <PageSection>
        <Stack gap="md">
          <Stepper active={step} onStepClick={setStep} allowNextStepsSelect={false} size="sm">
            <Stepper.Step label={t("fromOrders.step.pick")}>
              <Stack>
                {canViewCustomers && (
                  <CustomerPicker
                    label={t("fromOrders.addCustomer")}
                    value={picker}
                    onChange={(id) => {
                      setPicker(null);
                      if (id !== null && !customers.includes(id)) setCustomers((current) => [...current, id]);
                    }}
                  />
                )}
                {customers.length > 0 && (
                  <Group gap="xs">
                    {customers.map((id) => {
                      const name = orders.find((o) => o.customerId === id)?.customerName ?? String(id);
                      return (
                        <Badge
                          key={id}
                          variant="light"
                          rightSection={
                            <CloseButton
                              size="xs"
                              aria-label={t("fromOrders.removeCustomer", { name })}
                              onClick={() => setCustomers((current) => current.filter((c) => c !== id))}
                            />
                          }
                        >
                          {name}
                        </Badge>
                      );
                    })}
                  </Group>
                )}
                {customers.length === 0 && orderIds.length === 0 && (
                  <Text size="sm" c="dimmed">
                    {t("fromOrders.pickHint")}
                  </Text>
                )}
                {due.isError && (
                  <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("fromOrders.failedToLoad")}>
                    {refusalMessage(due.error, t, date)}
                  </Alert>
                )}
                {due.isFetching && !due.data && <ContentSkeleton rows={3} rowHeight={40} />}
                {due.data?.warnings.includes("orders_truncated") && (
                  <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
                    {t("fromOrders.truncated")}
                  </Alert>
                )}
                {due.data && orders.length === 0 && (
                  <Text size="sm" c="dimmed">
                    {t("fromOrders.noOrders")}
                  </Text>
                )}
                {orders.map((o) => (
                  <Group
                    key={keyOf(o)}
                    gap="sm"
                    wrap="wrap"
                    data-order={o.id}
                    data-kind={o.kind}
                    style={o.blocked ? { opacity: 0.6 } : undefined}
                  >
                    <Checkbox
                      aria-label={
                        isSubscription(o)
                          ? t("fromOrders.chooseSubscription", { number: o.number })
                          : t("fromOrders.chooseOrder", { number: o.number })
                      }
                      disabled={Boolean(o.blocked)}
                      checked={!o.blocked && !unchecked.has(keyOf(o))}
                      onChange={(e) => toggle(keyOf(o), e.currentTarget.checked)}
                    />
                    <Text size="sm" fw={600}>
                      {documentLabel(o, true)}
                    </Text>
                    {o.customerName && <Text size="sm">{o.customerName}</Text>}
                    {o.yourReference && (
                      <Text size="sm">{t("fromOrders.yourReference", { reference: o.yourReference })}</Text>
                    )}
                    {o.deliveryDate && (
                      <Text size="sm">{t("fromOrders.deliveryDate", { date: date(o.deliveryDate) })}</Text>
                    )}
                    <Text size="sm">
                      {money(
                        round2(
                          o.lines.reduce((sum, l) => sum + lineNet(l.toInvoice, l.unitPrice, l.discountPercent), 0),
                        ),
                        o.currency,
                      )}
                    </Text>
                    {o.blocked && (
                      <Text size="xs" c="orange" data-blocked={o.blocked}>
                        {`fromOrders.blocked.${o.blocked}` in invoicesCatalog.en
                          ? t(`fromOrders.blocked.${o.blocked}`)
                          : o.blocked}
                      </Text>
                    )}
                  </Group>
                ))}
              </Stack>
            </Stepper.Step>
            <Stepper.Step label={t("fromOrders.step.lines")}>
              <Stack>
                <Text size="sm" c="dimmed">
                  {t("fromOrders.partialHint")}
                </Text>
                {chosenOrders.map((o) => (
                  <Stack key={keyOf(o)} gap={4}>
                    <Text fw={600} size="sm">
                      {documentLabel(o)}
                    </Text>
                    <Table.ScrollContainer minWidth={640}>
                      <Table
                        aria-label={
                          isSubscription(o)
                            ? t("fromOrders.periodsOf", { number: o.number })
                            : t("fromOrders.linesOf", { number: o.number })
                        }
                        fz="sm"
                      >
                        <Table.Thead>
                          <Table.Tr>
                            <Table.Th>{t("fromOrders.col.line")}</Table.Th>
                            <Table.Th ta="right">{t("fromOrders.col.left")}</Table.Th>
                            <Table.Th ta="right">{t("fromOrders.col.toInvoice")}</Table.Th>
                            <Table.Th ta="right">{t("fromOrders.col.remaining")}</Table.Th>
                            <Table.Th ta="right">{t("unitPrice")}</Table.Th>
                            <Table.Th ta="right">{t("fromOrders.col.amount")}</Table.Th>
                          </Table.Tr>
                        </Table.Thead>
                        <Table.Tbody>
                          {o.lines.map((l) => {
                            const remaining = Math.round((l.toInvoice - quantityOf(l)) * 1000) / 1000;
                            return (
                              <Table.Tr key={`${l.lineId}:${l.periodStart ?? ""}`}>
                                <Table.Td>
                                  {l.description}
                                  {l.periodStart && (
                                    <Text span size="sm" c="dimmed">
                                      {` – ${periodLabel(l)}`}
                                    </Text>
                                  )}
                                  {remaining > 0 && quantityOf(l) > 0 && (
                                    <Badge ml="xs" size="xs" variant="light">
                                      {t("fromOrders.partial")}
                                    </Badge>
                                  )}
                                </Table.Td>
                                <Table.Td ta="right">{`${number(l.toInvoice, 3)} ${l.unit}`}</Table.Td>
                                <Table.Td ta="right">
                                  {l.periodStart ? (
                                    number(l.toInvoice, 3)
                                  ) : (
                                    <NumberInput
                                      aria-label={t("fromOrders.toInvoiceOf", { line: l.description })}
                                      value={quantities[l.lineId] ?? l.toInvoice}
                                      min={0}
                                      max={l.toInvoice}
                                      decimalScale={3}
                                      clampBehavior="strict"
                                      size="xs"
                                      w={110}
                                      ml="auto"
                                      onChange={(v) => setQuantities((current) => ({ ...current, [l.lineId]: v }))}
                                    />
                                  )}
                                </Table.Td>
                                <Table.Td ta="right" data-testid={`remaining-${l.lineId}`}>
                                  {number(remaining, 3)}
                                </Table.Td>
                                <Table.Td ta="right">{unitPrice(l.unitPrice, o.currency)}</Table.Td>
                                <Table.Td ta="right">{money(amountOf(l), o.currency)}</Table.Td>
                              </Table.Tr>
                            );
                          })}
                        </Table.Tbody>
                      </Table>
                    </Table.ScrollContainer>
                  </Stack>
                ))}
                <Text size="sm" fw={600} data-testid="from-orders-chosen">
                  {t("fromOrdersChosen", { count: chosenLines.length, amount: money(chosenAmount, "NOK") })}
                </Text>
              </Stack>
            </Stepper.Step>
            <Stepper.Step label={t("fromOrders.step.grouping")}>
              <Stack>
                <SegmentedControl
                  aria-label={t("fromOrders.step.grouping")}
                  value={mode}
                  onChange={(v) => setMode(v as Mode)}
                  data={[
                    { value: "perOrder", label: t("fromOrders.mode.perOrder") },
                    { value: "combined", label: t("fromOrders.mode.combined") },
                  ]}
                />
                {mode === "combined" && (
                  <Text size="sm" c="dimmed">
                    {t("fromOrders.combinedHint")}
                  </Text>
                )}
                <Text size="sm" fw={600}>
                  {t("fromOrdersPreview", { count: preview.length })}
                </Text>
                <Stack gap={4} data-testid="from-orders-preview">
                  {preview.map((p) => (
                    <Stack key={`${p.customerId}-${keyOf(p.orders[0])}`} gap={0}>
                      <Text size="sm">
                        {t("fromOrders.previewInvoice", {
                          customer: p.customerName || p.customerId,
                          orders: p.orders
                            .map((o) =>
                              isSubscription(o)
                                ? t("fromOrders.subscriptionShort", { number: o.number })
                                : String(o.number),
                            )
                            .join(", "),
                        })}
                      </Text>
                      {p.separatedBy.length > 0 && (
                        <Text size="xs" c="dimmed">
                          {t("fromOrders.separatedBy", {
                            fields: p.separatedBy.map((f) => t(`fromOrders.field.${f}`)).join(", "),
                          })}
                        </Text>
                      )}
                    </Stack>
                  ))}
                </Stack>
              </Stack>
            </Stepper.Step>
          </Stepper>
          {refusal && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("fromOrders.couldNotCreate")} role="alert">
              <Stack gap={4}>
                {refusal.map((words) => (
                  <Text key={words} size="sm">
                    {words}
                  </Text>
                ))}
              </Stack>
            </Alert>
          )}
          <Group justify="flex-end">
            {step > 0 && (
              <Button variant="default" onClick={() => setStep(step - 1)}>
                {t("fromOrders.back")}
              </Button>
            )}
            {step < 2 ? (
              <Button
                disabled={step === 0 ? chosenOrders.length === 0 : chosenLines.length === 0}
                onClick={() => setStep(step + 1)}
              >
                {t("fromOrders.next")}
              </Button>
            ) : (
              <Button disabled={chosenLines.length === 0} loading={create.isPending} onClick={() => create.mutate()}>
                {t("fromOrders.create")}
              </Button>
            )}
          </Group>
        </Stack>
      </PageSection>
    </Stack>
  );
};
