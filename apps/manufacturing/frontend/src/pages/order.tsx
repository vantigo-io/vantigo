import {
  Alert,
  Anchor,
  Badge,
  Button,
  Group,
  List,
  Modal,
  SimpleGrid,
  Stack,
  Table,
  Tabs,
  Text,
  Timeline,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconAlertTriangle,
  IconArrowsSplit,
  IconCalendarEvent,
  IconCalendarOff,
  IconCheck,
  IconCircleCheck,
  IconClipboardCheck,
  IconHierarchy,
  IconLock,
  IconPencil,
  IconPuzzle,
  IconX,
  IconZoomExclamation,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import {
  cancelOrder,
  confirmOrder,
  isOpenOrder,
  type ManufacturingOrder,
  type ManufacturingOrderEvent,
  type ManufacturingOrderWithShortLines,
  type ManufacturingShortLine,
  orderEventsQueryOptions,
  orderQueryOptions,
  reserveOrder,
  salesOrderLineQueryOptions,
  useOrdersLive,
} from "../api/orders";
import { isRevisionConflict, NotFoundError, refusalCode } from "../api/request";
import { planOrder, unplanOrder } from "../api/schedule";
import { manufacturingSitesQueryOptions } from "../api/sites";
import { ChildrenDialog } from "../components/children-dialog";
import { CloseDialog } from "../components/close-dialog";
import { CostPanel } from "../components/cost-panel";
import { DeviationDialog } from "../components/deviation-dialog";
import { OrderComponents } from "../components/order-components";
import { OrderForm } from "../components/order-form";
import { OrderLots } from "../components/order-lots";
import { ProductionDialog } from "../components/production-dialog";
import { SplitDialog } from "../components/split-dialog";
import { UnbuildDialog } from "../components/unbuild-dialog";
import { WorkOrdersPanel } from "../components/work-orders-panel";
import { useRecordActions } from "../lib/actions";
import { canManageOrders, canRegisterProduction } from "../lib/capabilities";
import { compareDecimal } from "../lib/decimal";
import {
  availabilityColor,
  orderStatusColor,
  refusalMessage,
  reservationColor,
  useInvalidateManufacturing,
  useManufacturingFormat,
} from "../lib/format";
import { MANUFACTURING_HOME_ROUTE_PATH, ORDERS_ROUTE_PATH, orderLinkOptions } from "../lib/routes";
import type { NewOrderSearch } from "../lib/search";
import { subAssemblyLines } from "../lib/sub-assembly";

type TFunction = ReturnType<typeof useManufacturingFormat>["t"];

const breadcrumbs = (t: TFunction, label: string) => [
  { label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH },
  { label: t("navOrders"), to: ORDERS_ROUTE_PATH },
  { label },
];

/**
 * A new manufacturing order's form (M17); it says so when the caller may not
 * create one. Opened from a sales order's line (`?salesOrderLineId=`, M45)
 * it is prefilled from the line, read through Orders, and made for it.
 */
export const NewOrderPage = () => {
  const { t } = useManufacturingFormat();
  const meta = useQuery(manufacturingMetaQueryOptions());
  const { salesOrderLineId } = useSearch({ strict: false }) as NewOrderSearch;
  const line = useQuery({
    ...salesOrderLineQueryOptions(salesOrderLineId ?? 0),
    enabled: salesOrderLineId !== undefined && canManageOrders(meta.data),
  });
  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={breadcrumbs(t, t("newOrder"))}
        title={t("newOrder")}
        description={t("newOrderDescription")}
      />
      {meta.data && !canManageOrders(meta.data) ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_manageOrders")}
        </Alert>
      ) : meta.isPending || (salesOrderLineId !== undefined && line.isPending) ? (
        <ContentSkeleton rows={4} />
      ) : salesOrderLineId !== undefined && line.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {line.error instanceof NotFoundError ? t("salesOrderLineNotFound") : line.error.message}
        </Alert>
      ) : (
        <PageSection>
          <OrderForm key={line.data?.lineId ?? 0} salesOrderLine={line.data} />
        </PageSection>
      )}
    </Stack>
  );
};

/** A label and its value, as the header's facts read. */
const Fact = ({ label, children }: { label: string; children: ReactNode }) => (
  <Stack gap={2}>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text size="sm" component="div">
      {children}
    </Text>
  </Stack>
);

const OrderLinkAnchor = ({ id, number }: { id: number; number: string }) => (
  <Anchor size="sm" renderRoot={(props) => <Link {...orderLinkOptions(id)} {...props} />}>
    {number}
  </Anchor>
);

/** The lines a reservation left short: from the last write that reserved, else those not fully reserved. */
const shortLinesOf = (
  order: ManufacturingOrder,
  answered: readonly ManufacturingShortLine[] | undefined,
): { sku: string; missing?: string; unit: string }[] => {
  if (!isOpenOrder(order) || order.reservationStatus === "reserved" || order.reservationStatus === null) return [];
  if (answered && answered.length > 0)
    return answered.map((line) => ({ sku: line.variant.sku, missing: line.missing, unit: line.variant.unit }));
  return order.components
    .filter((line) => compareDecimal(line.reserved, line.remaining) < 0)
    .map((line) => ({ sku: line.variant.sku, unit: line.variant.unit }));
};

/** What an event's payload says, its figures in the viewer's words. */
const payloadText = (event: ManufacturingOrderEvent, decimal: (text: string) => string) =>
  Object.entries(event.payload)
    .filter(([, value]) => typeof value === "string" || typeof value === "number" || typeof value === "boolean")
    .map(([key, value]) => {
      const shown = typeof value === "string" && /^-?\d+(\.\d+)?$/.test(value) ? decimal(value) : String(value);
      return `${key}: ${shown}`;
    })
    .join(" · ");

const History = ({ orderId, live }: { orderId: number; live: boolean }) => {
  const { t, stamp, decimal } = useManufacturingFormat();
  const events = useQuery(orderEventsQueryOptions(orderId, live));
  return (
    <PageSection title={t("history")}>
      <div data-assist="manufacturing.order.history">
        {events.error ? (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
            {events.error.message}
          </Alert>
        ) : events.isPending ? (
          <ContentSkeleton rows={2} />
        ) : events.data.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noHistory")}
          </Text>
        ) : (
          <Timeline bulletSize={14} lineWidth={2}>
            {events.data.map((event) => {
              const figures = payloadText(event, decimal);
              return (
                <Timeline.Item key={event.id} title={t(`event_${event.kind}`)}>
                  <Text size="xs" c="dimmed">
                    {t("eventBy", { who: event.actorDisplay, when: stamp(event.at) })}
                  </Text>
                  {figures && <Text size="sm">{figures}</Text>}
                </Timeline.Item>
              );
            })}
          </Timeline>
        )}
      </div>
    </PageSection>
  );
};

/**
 * One manufacturing order (M17–M26): its facts and status, the actions its
 * status and the caller's capabilities offer — Edit, Confirm, Reserve now,
 * Plan / Unplan (M35), Register production, Close, Cancel — its planned
 * window and how late it is planned to finish, its cost when the caller may
 * see it (M38–M41) — its component lines with what is
 * missing — beside its work orders and time logs once confirmed (M27, M29)
 * — the short-line notice, and its history. It follows
 * `manufacturing.order:<id>`, polling only while live is down.
 */
export const OrderPage = () => {
  const { t, quantity, day, stamp } = useManufacturingFormat();
  const { orderId } = useParams({ strict: false }) as { orderId: string };
  const id = Number(orderId);
  const live = useOrdersLive(id);
  const meta = useQuery(manufacturingMetaQueryOptions());
  const order = useQuery(orderQueryOptions(id, live));
  const sites = useQuery(manufacturingSitesQueryOptions());
  const invalidate = useInvalidateManufacturing();
  const { confirm } = useRecordActions();
  const [shortLines, setShortLines] = useState<readonly ManufacturingShortLine[]>();
  const [editing, setEditing] = useState(false);
  const [producing, setProducing] = useState(false);
  const [closing, setClosing] = useState(false);
  const [makingChildren, setMakingChildren] = useState(false);
  const [reporting, setReporting] = useState(false);
  const [splitting, setSplitting] = useState(false);
  const [unbuilding, setUnbuilding] = useState(false);
  const [busy, setBusy] = useState(false);
  const manage = canManageOrders(meta.data);
  const produce = canRegisterProduction(meta.data);
  const data = order.data;

  const reserving = async (work: () => Promise<ManufacturingOrderWithShortLines>, done: string, failure: string) => {
    setBusy(true);
    try {
      const answer = await work();
      setShortLines(answer.shortLines);
      notifications.show({
        color: answer.shortLines.length > 0 ? "yellow" : "teal",
        title: done,
        message: answer.shortLines.length > 0 ? t("someLinesShort", { count: answer.shortLines.length }) : "",
      });
      await invalidate();
    } catch (error) {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await invalidate();
        return;
      }
      notifications.show({ color: "red", title: failure, message: refusalMessage(t, error as Error) });
    } finally {
      setBusy(false);
    }
  };

  const cancel = (current: ManufacturingOrder) =>
    confirm(t("cancelOrderTitle", { number: current.number }), t("cancelOrderBody"), t("cancelOrder"), async () => {
      try {
        await cancelOrder(current.id, current.revision);
        notifications.show({ color: "teal", title: t("orderCancelled"), message: current.number });
        await invalidate();
      } catch (error) {
        if (isRevisionConflict(error)) {
          notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
          await invalidate();
          return;
        }
        notifications.show({ color: "red", title: t("couldNotCancel"), message: refusalMessage(t, error as Error) });
      }
    });

  /** Plan or Unplan (M35): a refusal for no room within 365 days says so in its own words. */
  const planning = async (work: () => Promise<ManufacturingOrder>, done: string, failure: string) => {
    setBusy(true);
    try {
      const answer = await work();
      notifications.show({ color: "teal", title: done, message: answer.number });
      await invalidate();
    } catch (error) {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await invalidate();
        return;
      }
      notifications.show({
        color: refusalCode(error) === "no_capacity" ? "yellow" : "red",
        title: failure,
        message: refusalMessage(t, error as Error),
      });
    } finally {
      setBusy(false);
    }
  };

  const short = data ? shortLinesOf(data, shortLines) : [];
  const site = data ? sites.data?.find((one) => one.id === data.siteId) : undefined;
  const canCancel = data && manage && (data.status === "draft" || isOpenOrder(data));
  const reserveButton = (current: ManufacturingOrder) => (
    <Button
      variant="default"
      leftSection={<IconLock size={16} />}
      loading={busy}
      onClick={() =>
        void reserving(() => reserveOrder(current.id, current.revision), t("orderReserved"), t("couldNotReserve"))
      }
    >
      {t("reserveNow")}
    </Button>
  );

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={breadcrumbs(t, data?.number ?? t("order"))}
        title={
          data ? (
            <Group gap="sm">
              {`${data.number} · ${data.variant.sku}`}
              <Badge variant="light" color={orderStatusColor[data.status]}>
                {t(`orderStatus_${data.status}`)}
              </Badge>
              {(data.status === "draft" || isOpenOrder(data)) && (
                <Badge variant="light" color={availabilityColor[data.availability]}>
                  {t(`availability_${data.availability}`)}
                </Badge>
              )}
              {data.priority === "urgent" && (
                <Badge variant="filled" color="red">
                  {t("priority_urgent")}
                </Badge>
              )}
              {data.late && (
                <Badge variant="light" color="red">
                  {t("late")}
                </Badge>
              )}
              {data.lateByDays !== null && data.lateByDays > 0 && (
                <Badge variant="filled" color="red">
                  {t("lateByDays", { count: data.lateByDays })}
                </Badge>
              )}
              {data.origin.cancelled && (
                <Badge variant="filled" color="orange" data-testid="origin-cancelled">
                  {t("originCancelled")}
                </Badge>
              )}
            </Group>
          ) : (
            t("order")
          )
        }
        description={data?.variant.productName}
        actions={
          data && (
            <Group gap="xs" data-assist="manufacturing.order.actions">
              {manage && data.status !== "done" && data.status !== "cancelled" && (
                <Button variant="default" leftSection={<IconPencil size={16} />} onClick={() => setEditing(true)}>
                  {t("edit")}
                </Button>
              )}
              {manage && data.status === "draft" && (
                <Button
                  leftSection={<IconCheck size={16} />}
                  loading={busy}
                  onClick={() =>
                    void reserving(
                      () => confirmOrder(data.id, data.revision),
                      t("orderConfirmed"),
                      t("couldNotConfirm"),
                    )
                  }
                >
                  {t("confirmOrder")}
                </Button>
              )}
              {manage && isOpenOrder(data) && data.reservationStatus !== "reserved" && reserveButton(data)}
              {manage && isOpenOrder(data) && (
                <Button
                  variant="default"
                  leftSection={<IconCalendarEvent size={16} />}
                  loading={busy}
                  data-assist="manufacturing.order.plan"
                  onClick={() =>
                    void planning(() => planOrder(data.id, data.revision), t("orderPlanned"), t("couldNotPlan"))
                  }
                >
                  {data.plannedStart ? t("replan") : t("plan")}
                </Button>
              )}
              {manage && isOpenOrder(data) && data.plannedStart && (
                <Button
                  variant="default"
                  leftSection={<IconCalendarOff size={16} />}
                  loading={busy}
                  onClick={() =>
                    void planning(() => unplanOrder(data.id, data.revision), t("orderUnplanned"), t("couldNotUnplan"))
                  }
                >
                  {t("unplan")}
                </Button>
              )}
              {produce && isOpenOrder(data) && (
                <Button leftSection={<IconClipboardCheck size={16} />} onClick={() => setProducing(true)}>
                  {t("registerProduction")}
                </Button>
              )}
              {manage && isOpenOrder(data) && subAssemblyLines(data).length > 0 && (
                <Button
                  variant="default"
                  leftSection={<IconHierarchy size={16} />}
                  data-assist="manufacturing.order.children"
                  onClick={() => setMakingChildren(true)}
                >
                  {t("createChildren")}
                </Button>
              )}
              {produce && data.status !== "draft" && (
                <Button
                  variant="default"
                  color="red"
                  leftSection={<IconZoomExclamation size={16} />}
                  data-assist="manufacturing.order.report-deviation"
                  onClick={() => setReporting(true)}
                >
                  {t("reportDeviation")}
                </Button>
              )}
              {manage && isOpenOrder(data) && (
                <Button variant="default" leftSection={<IconCircleCheck size={16} />} onClick={() => setClosing(true)}>
                  {t("closeOrder")}
                </Button>
              )}
              {manage &&
                (data.status === "draft" || data.status === "confirmed") &&
                compareDecimal(data.produced, "0") === 0 && (
                  <Button
                    variant="default"
                    leftSection={<IconArrowsSplit size={16} />}
                    data-assist="manufacturing.order.split"
                    onClick={() => setSplitting(true)}
                  >
                    {t("splitOrder")}
                  </Button>
                )}
              {manage && data.status === "done" && compareDecimal(data.produced, data.unbuilt) > 0 && (
                <Button
                  variant="default"
                  leftSection={<IconPuzzle size={16} />}
                  data-assist="manufacturing.order.unbuild"
                  onClick={() => setUnbuilding(true)}
                >
                  {t("unbuild")}
                </Button>
              )}
              {canCancel && compareDecimal(data.produced, "0") === 0 && (
                <Button variant="default" color="red" leftSection={<IconX size={16} />} onClick={() => cancel(data)}>
                  {t("cancelOrder")}
                </Button>
              )}
            </Group>
          )
        }
      />
      {order.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {order.error instanceof NotFoundError ? t("orderNotFound") : order.error.message}
        </Alert>
      ) : !data || meta.isPending ? (
        <ContentSkeleton rows={4} />
      ) : (
        <>
          {data.origin.cancelled && (
            <Alert color="orange" icon={<IconAlertTriangle size={16} />} title={t("originCancelledTitle")}>
              {t("originCancelledHint", { reference: data.origin.reference ?? "" })}
            </Alert>
          )}
          {data.warnings.includes("variant_discontinued") && (
            <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("variantDiscontinuedTitle")}>
              {t("variantDiscontinuedHint")}
            </Alert>
          )}
          {short.length > 0 && (
            <Alert
              color="yellow"
              icon={<IconAlertTriangle size={16} />}
              title={t("shortLinesTitle")}
              data-assist="manufacturing.order.short-lines"
            >
              <Stack gap="xs">
                <Text size="sm">{t("shortLinesHint")}</Text>
                <List size="sm">
                  {short.map((line) => (
                    <List.Item key={line.sku}>
                      {line.missing
                        ? t("shortLine", { sku: line.sku, missing: quantity(line.missing, line.unit) })
                        : line.sku}
                    </List.Item>
                  ))}
                </List>
                {manage && <Group>{reserveButton(data)}</Group>}
              </Stack>
            </Alert>
          )}
          <PageSection>
            <SimpleGrid cols={{ base: 2, sm: 3, md: 4 }} spacing="md" data-assist="manufacturing.order.facts">
              <Fact label={t("quantity")}>{quantity(data.quantity, data.variant.unit)}</Fact>
              <Fact label={t("produced")}>
                {quantity(data.produced, data.variant.unit)}
                {compareDecimal(data.overrun, "0") > 0 && (
                  <Text size="xs" c="orange">
                    {t("overrunValue", { quantity: quantity(data.overrun, data.variant.unit) })}
                  </Text>
                )}
                {compareDecimal(data.outputScrap, "0") > 0 && (
                  <Text size="xs" c="dimmed">
                    {t("outputScrapValue", { quantity: quantity(data.outputScrap, data.variant.unit) })}
                  </Text>
                )}
                {compareDecimal(data.unbuilt, "0") > 0 && (
                  <Text size="xs" c="dimmed">
                    {t("unbuiltValue", { quantity: quantity(data.unbuilt, data.variant.unit) })}
                  </Text>
                )}
              </Fact>
              <Fact label={t("dueDate")}>{data.dueDate ? day(data.dueDate) : "—"}</Fact>
              <Fact label={t("plannedDates")}>
                {data.plannedStart || data.plannedFinish
                  ? `${data.plannedStart ? stamp(data.plannedStart) : "—"} – ${data.plannedFinish ? stamp(data.plannedFinish) : "—"}`
                  : "—"}
              </Fact>
              {(data.requestedStart || data.requestedStartDefault) && (
                <Fact label={t("requestedStart")}>
                  {data.requestedStart
                    ? day(data.requestedStart)
                    : t("requestedStartDefaultValue", { date: day(data.requestedStartDefault ?? "") })}
                </Fact>
              )}
              <Fact label={t("priority")}>{t(`priority_${data.priority}`)}</Fact>
              <Fact label={t("responsible")}>{data.responsible?.displayName ?? "—"}</Fact>
              <Fact label={t("site")}>{site ? `${site.code} · ${site.name}` : data.siteId}</Fact>
              <Fact label={t("bom")}>{data.bomCode ?? t("bomWithoutCode")}</Fact>
              <Fact label={t("origin")}>
                {t(`origin_${data.origin.kind}`)}
                {data.origin.reference ? ` · ${data.origin.reference}` : ""}
                {data.origin.customerName && (
                  <Text size="xs" c="dimmed">
                    {data.origin.customerName}
                  </Text>
                )}
              </Fact>
              {data.parent && (
                <Fact label={t("parentOrder")}>
                  <OrderLinkAnchor id={data.parent.id} number={data.parent.number} />
                </Fact>
              )}
              {data.children.length > 0 && (
                <Fact label={t("childOrders")}>
                  <Group gap="xs">
                    {data.children.map((link) => (
                      <OrderLinkAnchor key={link.id} id={link.id} number={link.number} />
                    ))}
                  </Group>
                </Fact>
              )}
              {data.reservationStatus && (
                <Fact label={t("reservation")}>
                  <Badge variant="light" color={reservationColor[data.reservationStatus]}>
                    {t(`reservation_${data.reservationStatus}`)}
                  </Badge>
                </Fact>
              )}
              {data.backorderOf && (
                <Fact label={t("backorderOf")}>
                  <OrderLinkAnchor id={data.backorderOf.id} number={data.backorderOf.number} />
                </Fact>
              )}
              {data.backorders.length > 0 && (
                <Fact label={t("backorders")}>
                  <Group gap="xs">
                    {data.backorders.map((link) => (
                      <OrderLinkAnchor key={link.id} id={link.id} number={link.number} />
                    ))}
                  </Group>
                </Fact>
              )}
              {data.splitFrom && (
                <Fact label={t("splitFrom")}>
                  <OrderLinkAnchor id={data.splitFrom.id} number={data.splitFrom.number} />
                </Fact>
              )}
              {data.splitParts.length > 0 && (
                <Fact label={t("splitParts")}>
                  <Group gap="xs">
                    {data.splitParts.map((link) => (
                      <OrderLinkAnchor key={link.id} id={link.id} number={link.number} />
                    ))}
                  </Group>
                </Fact>
              )}
              {data.mergedInto && (
                <Fact label={t("mergedInto")}>
                  <OrderLinkAnchor id={data.mergedInto.id} number={data.mergedInto.number} />
                </Fact>
              )}
              {data.mergedFrom.length > 0 && (
                <Fact label={t("mergedFrom")}>
                  <Group gap="xs">
                    {data.mergedFrom.map((link) => (
                      <OrderLinkAnchor key={link.id} id={link.id} number={link.number} />
                    ))}
                  </Group>
                </Fact>
              )}
              {data.closedAt && <Fact label={t("closedAt")}>{stamp(data.closedAt)}</Fact>}
            </SimpleGrid>
            {data.note && (
              <Text size="sm" mt="md" style={{ whiteSpace: "pre-wrap" }}>
                {data.note}
              </Text>
            )}
          </PageSection>
          {data.outputs.length > 0 && (
            <PageSection title={t("byProducts")}>
              <Table.ScrollContainer minWidth={480}>
                <Table data-assist="manufacturing.order.byproducts">
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("byProduct")}</Table.Th>
                      <Table.Th ta="right">{t("expected")}</Table.Th>
                      <Table.Th ta="right">{t("produced")}</Table.Th>
                      <Table.Th ta="right">{t("costShare")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.outputs.map((output) => (
                      <Table.Tr key={output.id ?? output.variant.id}>
                        <Table.Td>
                          <Text size="sm">{output.variant.sku}</Text>
                          <Text size="xs" c="dimmed">
                            {output.variant.productName}
                          </Text>
                        </Table.Td>
                        <Table.Td ta="right">{quantity(output.expected, output.variant.unit)}</Table.Td>
                        <Table.Td ta="right">{quantity(output.produced, output.variant.unit)}</Table.Td>
                        <Table.Td ta="right">{`${output.costShare} %`}</Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            </PageSection>
          )}
          {data.cost && <CostPanel cost={data.cost} currency={meta.data?.currency} />}
          {data.status === "draft" ? (
            <OrderComponents
              order={data}
              canManage={manage}
              onShortLines={(answer) => setShortLines(answer.shortLines)}
            />
          ) : (
            <Tabs defaultValue="components" keepMounted={false}>
              <Tabs.List mb="md">
                <Tabs.Tab value="components">{t("components")}</Tabs.Tab>
                <Tabs.Tab value="work-orders" data-assist="manufacturing.order.work-orders-tab">
                  {t("workOrders")}
                </Tabs.Tab>
                <Tabs.Tab value="lots" data-assist="manufacturing.order.lots-tab">
                  {t("lotsTab")}
                </Tabs.Tab>
              </Tabs.List>
              <Tabs.Panel value="components">
                <OrderComponents
                  order={data}
                  canManage={manage}
                  onShortLines={(answer) => setShortLines(answer.shortLines)}
                />
              </Tabs.Panel>
              <Tabs.Panel value="work-orders">
                <WorkOrdersPanel order={data} live={live} canManage={manage} />
              </Tabs.Panel>
              <Tabs.Panel value="lots">
                <OrderLots order={data} />
              </Tabs.Panel>
            </Tabs>
          )}
          <History orderId={data.id} live={live} />
          <Modal
            opened={editing}
            onClose={() => setEditing(false)}
            title={t("editOrderTitle", { number: data.number })}
            size="xl"
          >
            {editing && (
              <OrderForm
                key={data.revision}
                order={data}
                onSaved={() => setEditing(false)}
                onCancel={() => setEditing(false)}
              />
            )}
          </Modal>
          <ProductionDialog order={data} opened={producing} onClose={() => setProducing(false)} />
          <CloseDialog order={data} opened={closing} onClose={() => setClosing(false)} />
          <ChildrenDialog order={data} opened={makingChildren} onClose={() => setMakingChildren(false)} />
          <DeviationDialog opened={reporting} onClose={() => setReporting(false)} orderId={data.id} />
          <SplitDialog order={data} opened={splitting} onClose={() => setSplitting(false)} />
          <UnbuildDialog order={data} opened={unbuilding} onClose={() => setUnbuilding(false)} />
        </>
      )}
    </Stack>
  );
};
