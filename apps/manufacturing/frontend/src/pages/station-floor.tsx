import { Alert, Badge, Button, Card, Group, SimpleGrid, Stack, Table, Text, Title } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconAlertTriangle,
  IconArrowLeft,
  IconCheck,
  IconClipboardCheck,
  IconLockOpen,
  IconPackageExport,
  IconPlayerPause,
  IconPlayerPlay,
  IconTrash,
  IconZoomExclamation,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { isStationSignOut, refusalCode } from "../api/request";
import {
  finishWorkOrder,
  isClosedWorkOrder,
  type ManufacturingShopFloorResult,
  type ManufacturingTimeLog,
  pauseWorkOrder,
  startWorkOrder,
  unblockWorkCenter,
  useWorkCenterLive,
  useWorkOrderLive,
  workCenterQueueQueryOptions,
  workOrderQueryOptions,
} from "../api/shop-floor";
import { workCentersQueryOptions } from "../api/work-centers";
import { BlockDialog } from "../components/block-dialog";
import { ChecksPanel } from "../components/checks-panel";
import { DeviationDialog } from "../components/deviation-dialog";
import { Markdown } from "../components/markdown";
import { ProductionDialog } from "../components/production-dialog";
import { QuantityDialog } from "../components/quantity-dialog";
import { ScrapDialog } from "../components/scrap-dialog";
import { WorkOrderCard } from "../components/work-order-card";
import { canWorkOnShopFloor } from "../lib/capabilities";
import {
  refusalMessage,
  reservationColor,
  useInvalidateManufacturing,
  useManufacturingFormat,
  workOrderStatusColor,
} from "../lib/format";
import { operationProgress, TOUCH_CARD_MIN_HEIGHT } from "../lib/shop-floor";
import { StationContext } from "../lib/station";
import { formatElapsed, LONG_RUNNING_MS, useElapsed } from "../lib/timer";

/** The big buttons' height: well past a 48 px touch target (M31). */
const ACTION_HEIGHT = 72;

/** A big button's label wraps to a second line rather than being cut off, in any language. */
const ACTION_STYLES = { label: { whiteSpace: "normal", textAlign: "center" } } as const;

type StationView =
  | { kind: "centers" }
  | { kind: "center"; id: number }
  | { kind: "workOrder"; id: number; centerId: number; askForce?: boolean };

/**
 * The shop floor on a shared station (manufacturing phase 2 M140): the
 * station's work centers, a center's queue and a work order with its actions,
 * for the active operator, whose token every request carries (the host sets
 * it with `setStationOperator`). It reads only what is open to stations
 * (M135) — no site, lot, image, photo or other page — and links nowhere:
 * every step opens in place.
 */
export const StationShopFloor = () => {
  const [view, setView] = useState<StationView>({ kind: "centers" });
  return (
    <StationContext.Provider value={true}>
      {view.kind === "centers" && <StationCenters onOpen={(id) => setView({ kind: "center", id })} />}
      {view.kind === "center" && (
        <StationQueue
          id={view.id}
          onBack={() => setView({ kind: "centers" })}
          onOpen={(id, askForce) => setView({ kind: "workOrder", id, centerId: view.id, askForce })}
        />
      )}
      {view.kind === "workOrder" && (
        <StationWorkOrder
          key={view.id}
          id={view.id}
          centerId={view.centerId}
          askForce={view.askForce}
          onBack={() => setView({ kind: "center", id: view.centerId })}
        />
      )}
    </StationContext.Provider>
  );
};

/** The station's work centers (M131): the server answers only those. */
const StationCenters = ({ onOpen }: { onOpen: (id: number) => void }) => {
  const { t } = useManufacturingFormat();
  const centers = useQuery(workCentersQueryOptions());
  if (centers.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {centers.error.message}
      </Alert>
    );
  if (!centers.data) return <ContentSkeleton rows={3} rowHeight={TOUCH_CARD_MIN_HEIGHT} />;
  if (centers.data.length === 0)
    return (
      <Alert color="yellow" icon={<IconAlertTriangle size={20} />} title={t("stationNoWorkCenters")}>
        {t("stationNoWorkCentersHint")}
      </Alert>
    );
  return (
    <Stack gap="md">
      <Title order={3}>{t("chooseWorkCenter")}</Title>
      <SimpleGrid cols={{ base: 1, sm: 2, md: 3 }} spacing="md">
        {centers.data.map((center) => (
          <Card
            key={center.id}
            withBorder
            padding="lg"
            mih={TOUCH_CARD_MIN_HEIGHT}
            renderRoot={(props) => (
              <button
                type="button"
                {...props}
                onClick={() => onOpen(center.id)}
                style={{ ...props.style, textAlign: "start", width: "100%" }}
              />
            )}
          >
            <Group justify="space-between">
              <Stack gap={2}>
                <Text size="xl" fw={700}>
                  {center.code}
                </Text>
                <Text size="lg">{center.name}</Text>
              </Stack>
              {center.workingState === "blocked" && (
                <Badge size="lg" color="red">
                  {t("workingState_blocked")}
                </Badge>
              )}
            </Group>
          </Card>
        ))}
      </SimpleGrid>
    </Stack>
  );
};

/**
 * A work center's queue at the station, with its block (M30, M31). Start here
 * on a group's work order starts it at this center; one that is still pending
 * opens with Start anyway asked, as the work order's own Start does.
 */
const StationQueue = ({
  id,
  onBack,
  onOpen,
}: {
  id: number;
  onBack: () => void;
  onOpen: (id: number, askForce?: boolean) => void;
}) => {
  const { t, stamp } = useManufacturingFormat();
  const live = useWorkCenterLive(id);
  const meta = useQuery(manufacturingMetaQueryOptions());
  const queue = useQuery(workCenterQueueQueryOptions(id, live));
  const invalidate = useInvalidateManufacturing();
  const [blocking, setBlocking] = useState(false);
  const [busy, setBusy] = useState(false);
  const work = canWorkOnShopFloor(meta.data);
  const data = queue.data;

  const unblock = async () => {
    setBusy(true);
    try {
      await unblockWorkCenter(id);
      notifications.show({ color: "teal", title: t("workCenterUnblocked"), message: "" });
      await invalidate();
    } catch (error) {
      if (!isStationSignOut(error))
        notifications.show({ color: "red", title: t("couldNotUnblock"), message: refusalMessage(t, error as Error) });
    } finally {
      setBusy(false);
    }
  };
  const startHere = async (workOrderId: number) => {
    setBusy(true);
    try {
      await startWorkOrder(workOrderId, false, id);
      notifications.show({ color: "teal", title: t("workOrderStarted"), message: "" });
      await invalidate();
      onOpen(workOrderId);
    } catch (error) {
      if (refusalCode(error) === "work_order_pending") {
        onOpen(workOrderId, true);
        return;
      }
      if (!isStationSignOut(error))
        notifications.show({ color: "red", title: t("couldNotDoThat"), message: refusalMessage(t, error as Error) });
      await invalidate();
    } finally {
      setBusy(false);
    }
  };

  return (
    <Stack gap="lg">
      <Group justify="space-between">
        <Group gap="sm">
          <Button size="lg" variant="default" leftSection={<IconArrowLeft size={20} />} onClick={onBack}>
            {t("stationBack")}
          </Button>
          <Title order={3}>{data ? `${data.workCenter.code} · ${data.workCenter.name}` : t("workCenter")}</Title>
        </Group>
        {data &&
          work &&
          (data.block ? (
            <Button
              size="lg"
              color="teal"
              leftSection={<IconLockOpen size={20} />}
              loading={busy}
              onClick={() => void unblock()}
            >
              {t("unblock")}
            </Button>
          ) : (
            <Button size="lg" color="red" leftSection={<IconPlayerPause size={20} />} onClick={() => setBlocking(true)}>
              {t("block")}
            </Button>
          ))}
      </Group>
      {queue.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {queue.error.message}
        </Alert>
      ) : !data ? (
        <ContentSkeleton rows={3} rowHeight={TOUCH_CARD_MIN_HEIGHT} />
      ) : (
        <>
          {data.block && (
            <Alert
              color="red"
              icon={<IconAlertTriangle size={20} />}
              title={t("blockedBecause", { reason: t(`reason_${data.block.reason}`) })}
            >
              {data.block.by
                ? t("blockedSinceBy", { when: stamp(data.block.since), name: data.block.by.displayName })
                : t("blockedSince", { when: stamp(data.block.since) })}
            </Alert>
          )}
          <Stack gap="md">
            <Title order={4}>{t("queue")}</Title>
            {data.items.length === 0 ? (
              <Text size="lg" c="dimmed">
                {t("queueEmpty")}
              </Text>
            ) : (
              data.items.map((workOrder) => (
                <WorkOrderCard key={workOrder.id} workOrder={workOrder} onOpen={() => onOpen(workOrder.id)} />
              ))
            )}
          </Stack>
          {(data.groupWork ?? []).length > 0 && (
            <Stack gap="md">
              <Title order={4}>{t("groupWork")}</Title>
              <Text size="md" c="dimmed">
                {t("groupWorkHint")}
              </Text>
              {(data.groupWork ?? []).map((workOrder) => (
                <Stack key={workOrder.id} gap="xs">
                  <WorkOrderCard workOrder={workOrder} onOpen={() => onOpen(workOrder.id)} />
                  {work && !data.block && (
                    <Group>
                      <Button
                        size="lg"
                        color="teal"
                        leftSection={<IconPlayerPlay size={20} />}
                        loading={busy}
                        onClick={() => void startHere(workOrder.id)}
                      >
                        {t("startHere")}
                      </Button>
                    </Group>
                  )}
                </Stack>
              ))}
            </Stack>
          )}
          <BlockDialog workCenter={data.workCenter} opened={blocking} onClose={() => setBlocking(false)} />
        </>
      )}
    </Stack>
  );
};

/** The operator's running log here, counted on the client from its start (M32). */
const RunningTimer = ({ log }: { log: ManufacturingTimeLog }) => {
  const { t } = useManufacturingFormat();
  const elapsed = useElapsed(log.startedAt);
  return (
    <Stack gap="xs">
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
 * One work order at the station (M31, M140): the work order page's actions —
 * Start, Pause, Finish, Register quantity or production, Report scrap, Report a
 * deviation — its checks, its instructions and its components, for the
 * active operator. The order's lots and the links to other pages are left out.
 * Every start, Start anyway too, names the center it was opened from
 * (`centerId`): the station is judged by it, and a group's work order starts
 * at that member.
 */
const StationWorkOrder = ({
  id,
  centerId,
  askForce: askedForce = false,
  onBack,
}: {
  id: number;
  centerId: number;
  askForce?: boolean;
  onBack: () => void;
}) => {
  const { t, quantity } = useManufacturingFormat();
  const live = useWorkOrderLive(id);
  const meta = useQuery(manufacturingMetaQueryOptions());
  const view = useQuery(workOrderQueryOptions(id, live));
  const invalidate = useInvalidateManufacturing();
  const [busy, setBusy] = useState<"start" | "pause" | "finish">();
  const [askForce, setAskForce] = useState(askedForce);
  const [producing, setProducing] = useState(false);
  const [scrapping, setScrapping] = useState(false);
  const [passing, setPassing] = useState(false);
  const [reporting, setReporting] = useState(false);
  const work = canWorkOnShopFloor(meta.data);
  const data = view.data;

  const act = async (
    kind: "start" | "pause" | "finish",
    run: () => Promise<ManufacturingShopFloorResult>,
    done: string,
  ) => {
    setBusy(kind);
    try {
      const answer = await run();
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
      if (!isStationSignOut(error))
        notifications.show({ color: "red", title: t("couldNotDoThat"), message: refusalMessage(t, error as Error) });
      await invalidate();
    } finally {
      setBusy(undefined);
    }
  };
  const start = (force?: boolean) =>
    void act("start", () => startWorkOrder(id, force, centerId), t("workOrderStarted"));
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

  const back = (
    <Button size="lg" variant="default" leftSection={<IconArrowLeft size={20} />} onClick={onBack} w="fit-content">
      {t("stationBack")}
    </Button>
  );
  if (view.error)
    return (
      <Stack gap="lg">
        {back}
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {view.error.message}
        </Alert>
      </Stack>
    );
  if (!data || meta.isPending)
    return (
      <Stack gap="lg">
        {back}
        <ContentSkeleton rows={4} />
      </Stack>
    );

  const { workOrder, myOpenLog } = data;
  const { order } = workOrder;
  const runningHere = myOpenLog !== null && myOpenLog.workOrderId === workOrder.id;
  const closed = isClosedWorkOrder(workOrder);
  const progress = operationProgress(workOrder);
  const finishable = !closed && workOrder.status !== "pending";
  const blocked = data.block !== null;

  return (
    <Stack gap="lg">
      {back}
      <Stack gap={4}>
        <Group gap="sm">
          <Title order={3}>{`${order.number} · ${workOrder.name}`}</Title>
          <Badge size="lg" variant="light" color={workOrderStatusColor[workOrder.status]}>
            {t(`workOrderStatus_${workOrder.status}`)}
          </Badge>
        </Group>
        <Text size="lg">{`${order.variant.sku} · ${order.variant.productName}`}</Text>
        <Text size="md" c="dimmed">
          {[
            t("doneOfQuantity", {
              done: quantity(progress.done),
              quantity: quantity(order.quantity, order.variant.unit),
            }),
            ...(closed ? [] : [t("remainingValue", { quantity: quantity(progress.remaining, order.variant.unit) })]),
          ].join(" · ")}
        </Text>
      </Stack>
      {data.block && (
        <Alert
          color="red"
          icon={<IconAlertTriangle size={20} />}
          title={t("blockedBecause", { reason: t(`reason_${data.block.reason}`) })}
        >
          {t("blockedCannotStart")}
        </Alert>
      )}
      {myOpenLog !== null && !runningHere && !closed && (
        <Alert color="yellow" icon={<IconAlertTriangle size={20} />} title={t("runningElsewhereTitle")}>
          {t("runningElsewhereHint")}
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
      {runningHere && <RunningTimer log={myOpenLog} />}
      {work && !closed ? (
        <SimpleGrid cols={{ base: 1, xs: 2, lg: 4 }} spacing="md">
          {!runningHere && (
            <Button
              h={ACTION_HEIGHT}
              styles={ACTION_STYLES}
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
              styles={ACTION_STYLES}
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
              styles={ACTION_STYLES}
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
              styles={ACTION_STYLES}
              size="xl"
              variant="default"
              leftSection={<IconPackageExport size={28} />}
              onClick={() => setPassing(true)}
            >
              {t("registerQuantity")}
            </Button>
          )}
          {finishable && workOrder.last && (
            <Button
              h={ACTION_HEIGHT}
              styles={ACTION_STYLES}
              size="xl"
              leftSection={<IconClipboardCheck size={28} />}
              onClick={() => setProducing(true)}
            >
              {t("registerProduction")}
            </Button>
          )}
          <Button
            h={ACTION_HEIGHT}
            styles={ACTION_STYLES}
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
            styles={ACTION_STYLES}
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
      <Stack gap="sm">
        <Title order={4}>{t("checksTitle")}</Title>
        <ChecksPanel workOrderId={workOrder.id} checks={workOrder.checks ?? []} canRecord={work && !closed} />
      </Stack>
      <Stack gap="sm">
        <Title order={4}>{t("instructions")}</Title>
        {data.instructions.trim() ? (
          <Markdown>{data.instructions}</Markdown>
        ) : (
          <Text size="lg" c="dimmed">
            {t("noInstructions")}
          </Text>
        )}
      </Stack>
      <Stack gap="sm">
        <Title order={4}>{t("componentsHere")}</Title>
        {data.components.length === 0 ? (
          <Text size="lg" c="dimmed">
            {t("noComponentsHere")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={480}>
            <Table fz="md" verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("component")}</Table.Th>
                  <Table.Th>{t("requiredColumn")}</Table.Th>
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
                    <Table.Td>{quantity(line.remaining, line.variant.unit)}</Table.Td>
                    <Table.Td>
                      <Badge
                        size="lg"
                        variant="light"
                        color={line.covered ? reservationColor.reserved : reservationColor.not_reserved}
                      >
                        {line.covered
                          ? t("covered")
                          : t("missingValue", { quantity: quantity(line.missing, line.variant.unit) })}
                      </Badge>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
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
