import { Alert, Anchor, Avatar, Badge, Button, Group, SimpleGrid, Stack, Table, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconAlertTriangle,
  IconCheck,
  IconClipboardCheck,
  IconPackageExport,
  IconPlayerPause,
  IconPlayerPlay,
  IconTrash,
  IconZoomExclamation,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection, usePresence } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { NotFoundError, refusalCode } from "../api/request";
import {
  finishWorkOrder,
  isClosedWorkOrder,
  type ManufacturingShopFloorResult,
  type ManufacturingTimeLog,
  pauseWorkOrder,
  startWorkOrder,
  useWorkOrderLive,
  useWorkOrderOrderLive,
  workOrderQueryOptions,
} from "../api/shop-floor";
import { ChecksPanel } from "../components/checks-panel";
import { DeviationDialog } from "../components/deviation-dialog";
import { Markdown } from "../components/markdown";
import { OrderLots } from "../components/order-lots";
import { ProductionDialog } from "../components/production-dialog";
import { QuantityDialog } from "../components/quantity-dialog";
import { ScrapDialog } from "../components/scrap-dialog";
import { canWorkOnShopFloor } from "../lib/capabilities";
import {
  refusalMessage,
  reservationColor,
  useInvalidateManufacturing,
  useManufacturingFormat,
  workOrderStatusColor,
} from "../lib/format";
import { orderLinkOptions, shopFloorWorkCenterLinkOptions, workOrderLinkOptions } from "../lib/routes";
import { shopFloorBreadcrumbs } from "../lib/shop-floor";
import { formatElapsed, LONG_RUNNING_MS, useElapsed } from "../lib/timer";

/** The big buttons' height: well past a 48 px touch target (M31). */
const ACTION_HEIGHT = 72;

/** A label and its value, at arm's length. */
const Fact = ({ label, children }: { label: string; children: ReactNode }) => (
  <Stack gap={2}>
    <Text size="sm" c="dimmed">
      {label}
    </Text>
    <Text size="lg" component="div">
      {children}
    </Text>
  </Stack>
);

/** The caller's running log here, counted on the client from its start (M32), with the 12-hour warning (M29). */
const RunningTimer = ({ log }: { log: ManufacturingTimeLog }) => {
  const { t } = useManufacturingFormat();
  const elapsed = useElapsed(log.startedAt);
  return (
    <Stack gap="xs" data-assist="manufacturing.work-order.timer">
      <Text size="sm" c="dimmed">
        {log.kind === "downtime" ? t("yourDowntime") : t("yourTime")}
      </Text>
      <Text fz={56} fw={700} ff="monospace" lh={1} role="timer" aria-label={t("yourTime")}>
        {formatElapsed(elapsed)}
      </Text>
      {(log.longRunning || elapsed > LONG_RUNNING_MS) && (
        <Alert color="orange" icon={<IconAlertTriangle size={20} />} title={t("longRunningTitle")}>
          {t("longRunningHint")}
        </Alert>
      )}
    </Stack>
  );
};

/**
 * One work order on the shop floor (M31): large Start, Pause and Finish
 * buttons by its status and the caller's permission (M33), the caller's
 * running timer, the operation's instructions, its components, Register
 * production on the last operation — posted alone, or, when asked, finishing
 * the operation with it in one transaction (M29) — and Report scrap; who runs it, and who else is looking
 * at it, live through presence (M32). Starting a pending one asks again and
 * starts it anyway with `force`; starting here while running elsewhere says
 * that it stops the other.
 */
export const WorkOrderPage = () => {
  const { t, quantity, day, stamp, minutes } = useManufacturingFormat();
  const { workOrderId } = useParams({ strict: false }) as { workOrderId: string };
  const id = Number(workOrderId);
  const live = useWorkOrderLive(id);
  const others = usePresence(`manufacturing.work-order:${id}`, "viewing");
  const meta = useQuery(manufacturingMetaQueryOptions());
  const view = useQuery(workOrderQueryOptions(id, live));
  useWorkOrderOrderLive(id, view.data?.workOrder.orderId);
  const invalidate = useInvalidateManufacturing();
  const [busy, setBusy] = useState<"start" | "pause" | "finish">();
  const [askForce, setAskForce] = useState(false);
  const [producing, setProducing] = useState(false);
  const [scrapping, setScrapping] = useState(false);
  const [passing, setPassing] = useState(false);
  const [reporting, setReporting] = useState(false);
  const work = canWorkOnShopFloor(meta.data);
  const data = view.data;

  const act = async (
    kind: "start" | "pause" | "finish",
    send: () => Promise<ManufacturingShopFloorResult>,
    done: string,
  ) => {
    setBusy(kind);
    try {
      const answer = await send();
      setAskForce(false);
      notifications.show({ color: "teal", title: done, message: "" });
      if (answer.closedLog)
        notifications.show({ color: "yellow", title: t("stoppedElsewhere"), message: t("stoppedElsewhereHint") });
      await invalidate();
    } catch (error) {
      if (kind === "start" && refusalCode(error) === "work_order_pending") {
        setAskForce(true);
        return;
      }
      notifications.show({ color: "red", title: t("couldNotDoThat"), message: refusalMessage(t, error as Error) });
      await invalidate();
    } finally {
      setBusy(undefined);
    }
  };

  const start = (force?: boolean) => void act("start", () => startWorkOrder(id, force), t("workOrderStarted"));
  const pause = () => void act("pause", () => pauseWorkOrder(id), t("workOrderPaused"));
  const finish = () =>
    modals.openConfirmModal({
      title: t("finishTitle"),
      children: <Text size="md">{t("finishBody")}</Text>,
      labels: { confirm: t("finish"), cancel: t("cancel") },
      confirmProps: { size: "lg" },
      cancelProps: { size: "lg" },
      onConfirm: () => void act("finish", () => finishWorkOrder(id), t("workOrderFinished")),
    });

  if (view.error)
    return (
      <Stack gap="lg">
        <PageHeader breadcrumbs={shopFloorBreadcrumbs(t, t("workOrder"))} title={t("workOrder")} />
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {view.error instanceof NotFoundError ? t("workOrderNotFound") : view.error.message}
        </Alert>
      </Stack>
    );
  if (!data || meta.isPending)
    return (
      <Stack gap="lg">
        <PageHeader breadcrumbs={shopFloorBreadcrumbs(t, t("workOrder"))} title={t("workOrder")} />
        <ContentSkeleton rows={4} />
      </Stack>
    );

  const { workOrder, myOpenLog } = data;
  const { order } = workOrder;
  const runningHere = myOpenLog !== null && myOpenLog.workOrderId === workOrder.id;
  const runningElsewhere = myOpenLog !== null && !runningHere;
  const closed = isClosedWorkOrder(workOrder);
  const finishable = !closed && workOrder.status !== "pending";
  const blocked = data.block !== null;
  const longRunningOthers = data.openLogs.filter((log) => log.longRunning && log.id !== myOpenLog?.id && log.user);

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={shopFloorBreadcrumbs(t, `${order.number} · ${workOrder.name}`)}
        title={
          <Group gap="sm">
            {`${order.number} · ${workOrder.name}`}
            <Badge size="lg" variant="light" color={workOrderStatusColor[workOrder.status]}>
              {t(`workOrderStatus_${workOrder.status}`)}
            </Badge>
            {order.priority === "urgent" && (
              <Badge size="lg" color="red">
                {t("priority_urgent")}
              </Badge>
            )}
          </Group>
        }
        description={`${order.variant.sku} · ${order.variant.productName}`}
      />
      {data.block && (
        <Alert
          color="red"
          icon={<IconAlertTriangle size={20} />}
          title={t("blockedBecause", { reason: t(`reason_${data.block.reason}`) })}
        >
          <Stack gap={4}>
            <Text size="md">{t("blockedCannotStart")}</Text>
            {data.block.by && (
              <Text size="md">
                {t("blockedSinceBy", { when: stamp(data.block.since), name: data.block.by.displayName })}
              </Text>
            )}
          </Stack>
        </Alert>
      )}
      {runningElsewhere && !closed && (
        <Alert color="yellow" icon={<IconAlertTriangle size={20} />} title={t("runningElsewhereTitle")}>
          <Stack gap="xs">
            <Text size="md">{t("runningElsewhereHint")}</Text>
            {myOpenLog.workOrderId !== null && (
              <Anchor
                size="md"
                renderRoot={(props) => <Link {...workOrderLinkOptions(myOpenLog.workOrderId ?? 0)} {...props} />}
              >
                {t("openOtherWorkOrder")}
              </Anchor>
            )}
          </Stack>
        </Alert>
      )}
      {askForce && (
        <Alert color="yellow" icon={<IconAlertTriangle size={20} />} title={t("workOrderPendingTitle")}>
          <Stack gap="sm">
            <Text size="md">{t("workOrderPendingHint")}</Text>
            <Group>
              <Button size="lg" color="yellow" loading={busy === "start"} onClick={() => start(true)}>
                {t("startAnyway")}
              </Button>
              <Button size="lg" variant="default" onClick={() => setAskForce(false)}>
                {t("cancel")}
              </Button>
            </Group>
          </Stack>
        </Alert>
      )}
      <PageSection>
        <Stack gap="lg">
          {runningHere && <RunningTimer log={myOpenLog} />}
          {work && !closed ? (
            <SimpleGrid cols={{ base: 1, xs: 2, md: 4 }} spacing="md" data-assist="manufacturing.work-order.actions">
              {!runningHere && (
                <Button
                  h={ACTION_HEIGHT}
                  size="xl"
                  color="teal"
                  leftSection={<IconPlayerPlay size={28} />}
                  loading={busy === "start"}
                  disabled={blocked || askForce}
                  onClick={() => start()}
                >
                  {t("start")}
                </Button>
              )}
              {runningHere && (
                <Button
                  h={ACTION_HEIGHT}
                  size="xl"
                  color="yellow"
                  leftSection={<IconPlayerPause size={28} />}
                  loading={busy === "pause"}
                  onClick={pause}
                >
                  {t("pause")}
                </Button>
              )}
              {finishable && (
                <Button
                  h={ACTION_HEIGHT}
                  size="xl"
                  variant={workOrder.last ? "default" : "filled"}
                  leftSection={<IconCheck size={28} />}
                  loading={busy === "finish"}
                  onClick={finish}
                >
                  {t("finish")}
                </Button>
              )}
              {finishable && !workOrder.last && (
                <Button
                  h={ACTION_HEIGHT}
                  size="xl"
                  variant="default"
                  leftSection={<IconPackageExport size={28} />}
                  data-assist="manufacturing.work-order.quantity"
                  onClick={() => setPassing(true)}
                >
                  {t("registerQuantity")}
                </Button>
              )}
              {finishable && workOrder.last && (
                <Button
                  h={ACTION_HEIGHT}
                  size="xl"
                  leftSection={<IconClipboardCheck size={28} />}
                  onClick={() => setProducing(true)}
                >
                  {t("registerProduction")}
                </Button>
              )}
              <Button
                h={ACTION_HEIGHT}
                size="xl"
                variant="default"
                color="red"
                leftSection={<IconTrash size={28} />}
                onClick={() => setScrapping(true)}
              >
                {t("reportScrap")}
              </Button>
              <Button
                h={ACTION_HEIGHT}
                size="xl"
                variant="default"
                color="red"
                leftSection={<IconZoomExclamation size={28} />}
                onClick={() => setReporting(true)}
              >
                {t("reportDeviation")}
              </Button>
            </SimpleGrid>
          ) : (
            !work &&
            !closed && (
              <Text size="md" c="dimmed">
                {t("notAllowed_shopFloor")}
              </Text>
            )
          )}
          {longRunningOthers.length > 0 && (
            <Alert color="orange" icon={<IconAlertTriangle size={20} />} title={t("longRunningTitle")}>
              {t("longRunningOthers", { names: longRunningOthers.map((log) => log.user?.displayName).join(", ") })}
            </Alert>
          )}
        </Stack>
      </PageSection>
      <PageSection title={t("checksTitle")}>
        <ChecksPanel workOrderId={workOrder.id} checks={workOrder.checks ?? []} canRecord={work && !closed} />
      </PageSection>
      <PageSection title={t("onThisWorkOrder")}>
        <Stack gap="md" data-assist="manufacturing.work-order.people">
          <Fact label={t("runningNow")}>
            {workOrder.operators.length === 0 ? (
              <Text size="lg" c="dimmed">
                {t("nobodyRunning")}
              </Text>
            ) : (
              <Stack gap={4}>
                {workOrder.operators.map((operator) => (
                  <Text key={operator.user.id} size="lg">
                    {t(operator.kind === "downtime" ? "operatorDowntimeSince" : "operatorSince", {
                      name: operator.user.displayName,
                      when: stamp(operator.startedAt),
                    })}
                  </Text>
                ))}
              </Stack>
            )}
          </Fact>
          <Fact label={t("othersHere")}>
            {others.length === 0 ? (
              <Text size="lg" c="dimmed">
                {t("nobodyElseHere")}
              </Text>
            ) : (
              <Group gap="sm">
                {others.map((member) => (
                  <Group key={member.userId} gap={6}>
                    <Avatar size="md" name={member.name} color="initials" />
                    <Text size="lg">{member.name}</Text>
                  </Group>
                ))}
              </Group>
            )}
          </Fact>
        </Stack>
      </PageSection>
      <PageSection>
        <SimpleGrid cols={{ base: 2, sm: 3, md: 4 }} spacing="md">
          <Fact label={t("order")}>
            <Anchor size="lg" renderRoot={(props) => <Link {...orderLinkOptions(order.id)} {...props} />}>
              {order.number}
            </Anchor>
          </Fact>
          <Fact label={t("remainingColumn")}>{quantity(order.remaining, order.variant.unit)}</Fact>
          <Fact label={t("dueDate")}>{order.dueDate ? day(order.dueDate) : "—"}</Fact>
          <Fact label={t("workCenter")}>
            <Anchor
              size="lg"
              renderRoot={(props) => <Link {...shopFloorWorkCenterLinkOptions(workOrder.workCenter.id)} {...props} />}
            >
              {`${workOrder.workCenter.code} · ${workOrder.workCenter.name}`}
            </Anchor>
          </Fact>
          <Fact label={t("operationName")}>
            {t("operationOf", { sequence: workOrder.sequence, name: workOrder.name })}
          </Fact>
          <Fact label={t("loggedOfExpected")}>
            {t("minutesOf", { logged: minutes(workOrder.loggedMinutes), expected: minutes(workOrder.expectedMinutes) })}
          </Fact>
          {workOrder.workCenterGroup && (
            <Fact label={t("workCenterGroup")}>{t("groupValue", { code: workOrder.workCenterGroup.code })}</Fact>
          )}
          {!workOrder.last && (
            <Fact label={t("quantityDone")}>
              {workOrder.sendAheadQuantity
                ? t("sendAheadValue", {
                    done: quantity(workOrder.quantityDone ?? "0", order.variant.unit),
                    ahead: quantity(workOrder.sendAheadQuantity, order.variant.unit),
                  })
                : quantity(workOrder.quantityDone ?? "0", order.variant.unit)}
            </Fact>
          )}
        </SimpleGrid>
      </PageSection>
      <PageSection title={t("instructions")}>
        <div data-assist="manufacturing.work-order.instructions">
          {data.instructions.trim() ? (
            <Markdown>{data.instructions}</Markdown>
          ) : (
            <Text size="lg" c="dimmed">
              {t("noInstructions")}
            </Text>
          )}
        </div>
      </PageSection>
      <PageSection title={t("componentsHere")}>
        {data.components.length === 0 ? (
          <Text size="lg" c="dimmed">
            {t("noComponentsHere")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={560}>
            <Table fz="md" verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("component")}</Table.Th>
                  <Table.Th>{t("requiredColumn")}</Table.Th>
                  <Table.Th>{t("consumedColumn")}</Table.Th>
                  <Table.Th>{t("remainingColumn")}</Table.Th>
                  <Table.Th>{t("reservation")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {data.components.map((line) => (
                  <Table.Tr key={line.id ?? line.variant.id}>
                    <Table.Td>
                      <Text size="md">{line.variant.sku}</Text>
                      <Text size="sm" c="dimmed">
                        {line.variant.productName}
                      </Text>
                    </Table.Td>
                    <Table.Td>{quantity(line.required, line.variant.unit)}</Table.Td>
                    <Table.Td>{quantity(line.consumed, line.variant.unit)}</Table.Td>
                    <Table.Td>{quantity(line.remaining, line.variant.unit)}</Table.Td>
                    <Table.Td>
                      {line.covered ? (
                        <Badge size="lg" variant="light" color={reservationColor.reserved}>
                          {t("covered")}
                        </Badge>
                      ) : (
                        <Badge size="lg" variant="light" color={reservationColor.not_reserved}>
                          {t("missingValue", { quantity: quantity(line.missing, line.variant.unit) })}
                        </Badge>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </PageSection>
      <PageSection title={t("lotsTab")}>
        <OrderLots order={order} />
      </PageSection>
      <ProductionDialog
        order={{ id: order.id, number: order.number, siteId: workOrder.workCenter.siteId }}
        opened={producing}
        onClose={() => setProducing(false)}
        finish={(production) => finishWorkOrder(id, { production }).then((answer) => answer.production)}
      />
      <DeviationDialog
        opened={reporting}
        onClose={() => setReporting(false)}
        orderId={order.id}
        workOrderId={workOrder.id}
      />
      <QuantityDialog
        workOrderId={id}
        orderNumber={order.number}
        unit={order.variant.unit}
        opened={passing}
        onClose={() => setPassing(false)}
      />
      <ScrapDialog
        workOrderId={id}
        orderNumber={order.number}
        unit={order.variant.unit}
        components={data.components}
        opened={scrapping}
        onClose={() => setScrapping(false)}
      />
    </Stack>
  );
};
