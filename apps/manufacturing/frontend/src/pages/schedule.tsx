import {
  Alert,
  Anchor,
  Badge,
  Box,
  Button,
  Group,
  List,
  Modal,
  SegmentedControl,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  UnstyledButton,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCalendarEvent, IconChevronLeft, IconChevronRight, IconFlame } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection, useTimeZones } from "@vantigo/frontend-shell";
import { type PointerEvent as ReactPointerEvent, useRef, useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { isRevisionConflict } from "../api/request";
import {
  type ManufacturingPlanAllResult,
  type ManufacturingSchedule,
  type ManufacturingScheduleBar,
  type ManufacturingScheduleDay,
  type ManufacturingScheduleOrder,
  type ManufacturingScheduleRow,
  planAllOrders,
  planOrder,
  rescheduleWorkOrder,
  type ScheduleSpan,
  scheduleQueryOptions,
  useScheduleLive,
} from "../api/schedule";
import { manufacturingSitesQueryOptions } from "../api/sites";
import { canManageOrders } from "../lib/capabilities";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { MANUFACTURING_HOME_ROUTE_PATH, orderLinkOptions } from "../lib/routes";
import {
  addDays,
  barLayout,
  type DayWindow,
  dayWindows,
  draggedStart,
  droppedMember,
  movedStart,
  moveToPrefill,
  rescheduleBody,
  workOrderStart,
} from "../lib/schedule";
import { readRememberedScheduleSite, rememberScheduleSite } from "../lib/schedule-storage";

type TFunction = ReturnType<typeof useManufacturingFormat>["t"];

/** How far the pointer must travel before a press on a bar is a drag rather than a click. */
const DRAG_THRESHOLD_PX = 4;
/** The width of the work center column and of one day, so two weeks scroll inside the board, not the page. */
const LABEL_WIDTH = 160;
const DAY_MIN_WIDTH = 96;
const ROW_HEIGHT = 64;
const BAR_TOP = 24;
const BAR_HEIGHT = 32;

/** The reschedule of one bar, with what the board shows while it waits. */
const useReschedule = () => {
  const { t } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  return async (bar: ManufacturingScheduleBar, start: number, workCenterId?: number): Promise<boolean> => {
    try {
      await rescheduleWorkOrder(bar.workOrderId, rescheduleBody(bar, start, workCenterId));
      notifications.show({ color: "teal", title: t("workOrderRescheduled"), message: bar.orderNumber });
      await invalidate();
      return true;
    } catch (error) {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await invalidate();
        return false;
      }
      notifications.show({ color: "red", title: t("couldNotReschedule"), message: refusalMessage(t, error as Error) });
      return false;
    }
  };
};

/** One day's load in a row (M36): the server's per cent, marked above 100. */
const LoadLabel = ({ day }: { day: ManufacturingScheduleDay }) => {
  const { t, decimal } = useManufacturingFormat();
  if (day.closed)
    return (
      <Text size="xs" c="dimmed">
        {t(`closed_${day.closed}`)}
      </Text>
    );
  if (day.loadPercent === null) return null;
  return (
    <Text
      size="xs"
      fw={day.overloaded ? 700 : undefined}
      c={day.overloaded ? "red" : "dimmed"}
      aria-label={
        day.overloaded
          ? t("loadOverloaded", { percent: decimal(day.loadPercent) })
          : t("loadValue", { percent: decimal(day.loadPercent) })
      }
    >
      {`${decimal(day.loadPercent)} %`}
      {day.overloaded ? " !" : ""}
    </Text>
  );
};

interface BarProps {
  bar: ManufacturingScheduleBar;
  /** The work center of the row it is drawn on. */
  centerId: number;
  windows: readonly DayWindow[];
  canMove: boolean;
  onOpen: (bar: ManufacturingScheduleBar, centerId: number) => void;
  onDrop: (bar: ManufacturingScheduleBar, start: number, workCenterId?: number) => void;
}

/** The work center of the board row under a point, from its `data-schedule-row`. */
const rowAt = (x: number, y: number): number | undefined => {
  for (const element of document.elementsFromPoint?.(x, y) ?? []) {
    const row = (element as HTMLElement).closest?.("[data-schedule-row]");
    if (row) return Number(row.getAttribute("data-schedule-row"));
  }
  return undefined;
};

/**
 * A work order's slot (M36): its MO and product, urgent marked, late in the
 * danger colour. Whoever may plan drags it along its row — pointer events,
 * snapped to the quarter hour — or presses it for Move to…; others follow
 * it to the order.
 */
const Bar = ({ bar, centerId, windows, canMove, onOpen, onDrop }: BarProps) => {
  const { t, stamp, time } = useManufacturingFormat();
  const layout = barLayout(bar, windows);
  const drag = useRef<{ x: number; y: number; width: number; moved: boolean } | null>(null);
  const dragged = useRef(false);
  const [delta, setDelta] = useState<number | null>(null);
  const [lift, setLift] = useState(0);
  // A group work order whose member is not fixed may go down or up to another member's row (M119).
  const crossRows = (bar.memberIds ?? []).length > 0;
  const label = `${bar.orderNumber} · ${bar.variant.productName}`;
  const title = t("barTitle", {
    order: bar.orderNumber,
    product: `${bar.variant.sku} · ${bar.variant.productName}`,
    operation: bar.name,
    from: stamp(bar.startsAt),
    to: stamp(bar.endsAt),
  });
  const color = bar.late ? "var(--mantine-color-red-6)" : "var(--mantine-color-blue-6)";
  const preview = delta === null ? null : draggedStart(bar, delta, windows);

  const pointerDown = (event: ReactPointerEvent<HTMLElement>) => {
    if (!canMove || event.button !== 0) return;
    const track = event.currentTarget.parentElement;
    if (!track) return;
    drag.current = { x: event.clientX, y: event.clientY, width: track.getBoundingClientRect().width, moved: false };
    event.currentTarget.setPointerCapture?.(event.pointerId);
  };
  const pointerMove = (event: ReactPointerEvent<HTMLElement>) => {
    const current = drag.current;
    if (!current || current.width <= 0) return;
    const travelled = event.clientX - current.x;
    const dropped = crossRows ? event.clientY - current.y : 0;
    if (!current.moved && Math.abs(travelled) < DRAG_THRESHOLD_PX && Math.abs(dropped) < DRAG_THRESHOLD_PX) return;
    current.moved = true;
    setDelta(travelled / current.width);
    setLift(dropped);
  };
  const pointerUp = (event: ReactPointerEvent<HTMLElement>) => {
    const current = drag.current;
    drag.current = null;
    event.currentTarget.releasePointerCapture?.(event.pointerId);
    if (!current?.moved || current.width <= 0) return;
    dragged.current = true;
    setDelta(null);
    setLift(0);
    const member = crossRows ? droppedMember(bar, centerId, rowAt(event.clientX, event.clientY)) : undefined;
    onDrop(bar, workOrderStart(bar, draggedStart(bar, (event.clientX - current.x) / current.width, windows)), member);
  };
  const click = () => {
    // A drag ends in a click as well; only a press that did not move opens the dialog.
    if (dragged.current) {
      dragged.current = false;
      return;
    }
    onOpen(bar, centerId);
  };

  const left = layout.left + (delta ?? 0);
  const style = {
    position: "absolute" as const,
    top: BAR_TOP,
    height: BAR_HEIGHT,
    left: `${left * 100}%`,
    width: `max(${layout.width * 100}%, 6px)`,
    background: color,
    color: "white",
    borderRadius: 4,
    padding: "0 4px",
    overflow: "hidden",
    whiteSpace: "nowrap" as const,
    textOverflow: "ellipsis",
    fontSize: 12,
    lineHeight: `${BAR_HEIGHT}px`,
    cursor: canMove ? (delta === null ? "grab" : "grabbing") : "pointer",
    touchAction: canMove ? ("none" as const) : undefined,
    opacity: delta === null ? 1 : 0.85,
    transform: lift === 0 ? undefined : `translateY(${lift}px)`,
    zIndex: delta === null ? 1 : 2,
    outline: bar.urgent ? "2px solid var(--mantine-color-orange-5)" : undefined,
    textDecoration: "none",
  };
  const content = (
    <>
      {bar.urgent && <IconFlame size={12} aria-label={t("priority_urgent")} style={{ verticalAlign: "middle" }} />}{" "}
      {preview !== null ? time(new Date(preview).toISOString()) : label}
    </>
  );

  if (!canMove)
    return (
      <Box
        renderRoot={(props) => <Link {...orderLinkOptions(bar.orderId)} {...props} />}
        title={title}
        aria-label={title}
        data-assist="manufacturing.schedule.bar"
        style={style}
      >
        {content}
      </Box>
    );
  return (
    <UnstyledButton
      title={title}
      aria-label={t("moveBar", { title })}
      data-assist="manufacturing.schedule.bar"
      onPointerDown={pointerDown}
      onPointerMove={pointerMove}
      onPointerUp={pointerUp}
      onPointerCancel={() => {
        drag.current = null;
        setDelta(null);
        setLift(0);
      }}
      onClick={click}
      style={style}
    >
      {content}
    </UnstyledButton>
  );
};

/** One work center's row: its days, greyed where closed or blocked, its load, and its bars. */
const Row = ({
  row,
  windows,
  canMove,
  onOpen,
  onDrop,
}: {
  row: ManufacturingScheduleRow;
  windows: readonly DayWindow[];
  canMove: boolean;
  onOpen: (bar: ManufacturingScheduleBar, centerId: number) => void;
  onDrop: (bar: ManufacturingScheduleBar, start: number, workCenterId?: number) => void;
}) => {
  const { t } = useManufacturingFormat();
  const greyed = "var(--mantine-color-gray-light)";
  return (
    <Box
      data-schedule-row={row.workCenter.id}
      style={{ display: "flex", borderTop: "1px solid var(--mantine-color-default-border)" }}
    >
      <Box w={LABEL_WIDTH} p={6} style={{ flex: "none", background: row.blocked ? greyed : undefined }}>
        <Text size="sm" fw={600} truncate>
          {row.workCenter.code}
        </Text>
        <Text size="xs" c="dimmed" truncate>
          {row.workCenter.name}
        </Text>
        {row.blocked && (
          <Badge size="xs" color="red" variant="light">
            {t("workingState_blocked")}
          </Badge>
        )}
      </Box>
      <Box style={{ position: "relative", flex: 1, height: ROW_HEIGHT }}>
        <Box style={{ display: "flex", position: "absolute", inset: 0 }}>
          {windows.map((window) => {
            const day = row.days.find((one) => one.date === window.date);
            return (
              <Box
                key={window.date}
                px={4}
                style={{
                  flex: 1,
                  borderLeft: "1px solid var(--mantine-color-default-border)",
                  background: row.blocked || day?.closed ? greyed : undefined,
                }}
                data-closed={day?.closed ?? undefined}
              >
                {day && <LoadLabel day={day} />}
              </Box>
            );
          })}
        </Box>
        {row.bars.map((bar) => (
          <Bar
            key={`${bar.workOrderId}-${bar.startsAt}`}
            bar={bar}
            centerId={row.workCenter.id}
            windows={windows}
            canMove={canMove}
            onOpen={onOpen}
            onDrop={onDrop}
          />
        ))}
      </Box>
    </Box>
  );
};

/** The board itself: the day headers, then a row per work center. */
const Board = ({
  schedule,
  canMove,
  onOpen,
}: {
  schedule: ManufacturingSchedule;
  canMove: boolean;
  onOpen: (bar: ManufacturingScheduleBar, centerId: number) => void;
}) => {
  const { t, formatters } = useManufacturingFormat();
  const reschedule = useReschedule();
  const { businessTimeZone } = useTimeZones();
  const windows = dayWindows(schedule, businessTimeZone);
  if (schedule.workCenters.length === 0)
    return <EmptyState title={t("scheduleNoWorkCenters")} description={t("scheduleNoWorkCentersHint")} />;
  return (
    <Box style={{ overflowX: "auto" }} data-assist="manufacturing.schedule.board">
      <Box miw={LABEL_WIDTH + windows.length * DAY_MIN_WIDTH}>
        <Box style={{ display: "flex" }}>
          <Box w={LABEL_WIDTH} style={{ flex: "none" }} />
          {windows.map((window) => (
            <Box key={window.date} px={4} py={4} style={{ flex: 1 }}>
              <Text
                size="xs"
                fw={window.date === schedule.today ? 700 : 500}
                c={window.date === schedule.today ? "blue" : undefined}
              >
                {formatters.formatDate(`${window.date}T00:00:00Z`, {
                  weekday: "short",
                  day: "numeric",
                  month: "short",
                  timeZone: "UTC",
                })}
              </Text>
            </Box>
          ))}
        </Box>
        {schedule.workCenters.map((row) => (
          <Row
            key={row.workCenter.id}
            row={row}
            windows={windows}
            canMove={canMove}
            onOpen={onOpen}
            onDrop={(bar, start, workCenterId) => void reschedule(bar, start, workCenterId)}
          />
        ))}
      </Box>
    </Box>
  );
};

/** A bar being moved through Move to…, with the row it was opened on. */
interface Moving {
  bar: ManufacturingScheduleBar;
  centerId: number;
}

/**
 * Move to… (M36): the keyboard's way to drag a bar — a day and a time, the
 * same request; for a group work order whose member is not fixed, also the
 * member's row to move it to (manufacturing phase 2 M119).
 */
const MoveDialog = ({
  moving,
  rows,
  onClose,
}: {
  moving: Moving | undefined;
  rows: readonly ManufacturingScheduleRow[];
  onClose: () => void;
}) => {
  const { t, stamp } = useManufacturingFormat();
  const reschedule = useReschedule();
  const { timeZone } = useTimeZones();
  const bar = moving?.bar;
  const [values, setValues] = useState(() => (bar ? moveToPrefill(bar, timeZone) : { date: "", time: "" }));
  const [center, setCenter] = useState(moving?.centerId);
  const [busy, setBusy] = useState(false);
  if (!bar || !moving) return null;
  const members = [moving.centerId, ...(bar.memberIds ?? [])];
  const centerOptions = members.map((id) => {
    const row = rows.find((one) => one.workCenter.id === id);
    return { value: String(id), label: row ? `${row.workCenter.code} · ${row.workCenter.name}` : String(id) };
  });
  const valid = /^\d{4}-\d{2}-\d{2}$/.test(values.date) && /^\d{2}:\d{2}$/.test(values.time);
  const submit = async () => {
    if (!valid) return;
    setBusy(true);
    const moved = await reschedule(
      bar,
      movedStart(values.date, values.time, timeZone),
      center !== undefined && center !== moving.centerId ? center : undefined,
    );
    setBusy(false);
    if (moved) onClose();
  };
  return (
    <Modal opened onClose={onClose} title={t("moveToTitle", { order: bar.orderNumber, operation: bar.name })}>
      <Stack gap="md">
        <Text size="sm">
          {`${bar.variant.sku} · ${bar.variant.productName}`}
          <br />
          {t("barPlanned", { from: stamp(bar.startsAt), to: stamp(bar.endsAt) })}
        </Text>
        <Anchor size="sm" renderRoot={(props) => <Link {...orderLinkOptions(bar.orderId)} {...props} />}>
          {t("openOrder", { number: bar.orderNumber })}
        </Anchor>
        <Group grow>
          <TextInput
            type="date"
            label={t("moveToDate")}
            value={values.date}
            onChange={(event) => {
              const value = event.currentTarget.value;
              setValues((current) => ({ ...current, date: value }));
            }}
          />
          <TextInput
            type="time"
            step={900}
            label={t("moveToTime")}
            value={values.time}
            onChange={(event) => {
              const value = event.currentTarget.value;
              setValues((current) => ({ ...current, time: value }));
            }}
          />
        </Group>
        {(bar.memberIds ?? []).length > 0 && (
          <Select
            label={t("moveToWorkCenter")}
            allowDeselect={false}
            data={centerOptions}
            value={center === undefined ? null : String(center)}
            onChange={(next) => next && setCenter(Number(next))}
          />
        )}
        <Text size="xs" c="dimmed">
          {t("moveToHint")}
        </Text>
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={busy} disabled={!valid} onClick={() => void submit()}>
            {t("moveTo")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** What Plan all left unplanned for want of room within 365 days. */
const PlanAllResult = ({ result, onClose }: { result: ManufacturingPlanAllResult; onClose: () => void }) => {
  const { t } = useManufacturingFormat();
  return (
    <Alert
      color={result.noCapacity.length > 0 ? "yellow" : "teal"}
      icon={<IconAlertCircle size={16} />}
      withCloseButton
      onClose={onClose}
      title={t("planAllDone", { count: result.planned.length })}
    >
      {result.noCapacity.length > 0 && (
        <Stack gap={4}>
          <Text size="sm">{t("planAllNoCapacity")}</Text>
          <List size="sm">
            {result.noCapacity.map((link) => (
              <List.Item key={link.id}>
                <Anchor size="sm" renderRoot={(props) => <Link {...orderLinkOptions(link.id)} {...props} />}>
                  {link.number}
                </Anchor>
              </List.Item>
            ))}
          </List>
        </Stack>
      )}
    </Alert>
  );
};

/** The confirmed orders with no plan (M36), each with Plan for whoever may plan. */
const Unscheduled = ({
  orders,
  siteId,
  canPlan,
}: {
  orders: readonly ManufacturingScheduleOrder[];
  siteId: number;
  canPlan: boolean;
}) => {
  const { t, quantity, day, minutes } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const [busy, setBusy] = useState<number | "all">();
  const [result, setResult] = useState<ManufacturingPlanAllResult>();

  const fail = async (error: unknown, title: string) => {
    if (isRevisionConflict(error)) {
      notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
      await invalidate();
      return;
    }
    notifications.show({ color: "red", title, message: refusalMessage(t, error as Error) });
  };
  const plan = async (order: ManufacturingScheduleOrder) => {
    setBusy(order.id);
    try {
      await planOrder(order.id, order.revision);
      notifications.show({ color: "teal", title: t("orderPlanned"), message: order.number });
      await invalidate();
    } catch (error) {
      await fail(error, t("couldNotPlan"));
    } finally {
      setBusy(undefined);
    }
  };
  const planAll = async () => {
    setBusy("all");
    try {
      setResult(await planAllOrders(siteId));
      await invalidate();
    } catch (error) {
      await fail(error, t("couldNotPlan"));
    } finally {
      setBusy(undefined);
    }
  };

  return (
    <PageSection
      title={t("unscheduled")}
      actions={
        canPlan && (
          <Button
            leftSection={<IconCalendarEvent size={16} />}
            loading={busy === "all"}
            disabled={orders.length === 0}
            data-assist="manufacturing.schedule.plan-all"
            onClick={() => void planAll()}
          >
            {t("planAll")}
          </Button>
        )
      }
    >
      <Stack gap="md" data-assist="manufacturing.schedule.unscheduled">
        {result && <PlanAllResult result={result} onClose={() => setResult(undefined)} />}
        {orders.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("nothingUnscheduled")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={720}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("orderNumber")}</Table.Th>
                  <Table.Th>{t("product")}</Table.Th>
                  <Table.Th>{t("quantity")}</Table.Th>
                  <Table.Th>{t("dueDate")}</Table.Th>
                  <Table.Th>{t("requestedStart")}</Table.Th>
                  <Table.Th>{t("expectedTime")}</Table.Th>
                  {canPlan && <Table.Th />}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {orders.map((order) => (
                  <Table.Tr key={order.id}>
                    <Table.Td>
                      <Group gap={4} wrap="nowrap">
                        <Anchor size="sm" renderRoot={(props) => <Link {...orderLinkOptions(order.id)} {...props} />}>
                          {order.number}
                        </Anchor>
                        {order.priority === "urgent" && (
                          <Badge size="xs" color="red">
                            {t("priority_urgent")}
                          </Badge>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td>{`${order.variant.sku} · ${order.variant.productName}`}</Table.Td>
                    <Table.Td>{quantity(order.quantity, order.variant.unit)}</Table.Td>
                    <Table.Td>{order.dueDate ? day(order.dueDate) : "—"}</Table.Td>
                    <Table.Td>{order.requestedStart ? day(order.requestedStart) : "—"}</Table.Td>
                    <Table.Td>{minutes(order.expectedMinutes)}</Table.Td>
                    {canPlan && (
                      <Table.Td>
                        <Button
                          size="xs"
                          variant="light"
                          loading={busy === order.id}
                          disabled={busy !== undefined && busy !== order.id}
                          aria-label={t("planOrder", { number: order.number })}
                          onClick={() => void plan(order)}
                        >
                          {t("plan")}
                        </Button>
                      </Table.Td>
                    )}
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
    </PageSection>
  );
};

const breadcrumbs = (t: TFunction) => [
  { label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH },
  { label: t("navSchedule") },
];

/**
 * The production schedule (M36): a site's active work centers, a row each,
 * over a week or two; each work order's slots as bars at their planned
 * times, the load per day, closed days and blocked centers greyed; and the
 * confirmed orders with no plan. Whoever may manage orders plans them, one
 * or all, and moves a bar by dragging it along its row or through Move to…;
 * a group work order whose member is not fixed may also be dropped on another
 * member's row (manufacturing phase 2 M119). The site last looked at is
 * remembered in the browser. It follows `manufacturing.schedule:<siteId>`
 * and `manufacturing.orders`, polling while either is down.
 */
export const SchedulePage = () => {
  const { t } = useManufacturingFormat();
  const meta = useQuery(manufacturingMetaQueryOptions());
  const sites = useQuery(manufacturingSitesQueryOptions());
  const [chosenSite, setChosenSite] = useState<number | undefined>(readRememberedScheduleSite);
  const [from, setFrom] = useState<string>();
  const [days, setDays] = useState<ScheduleSpan>(7);
  const [moving, setMoving] = useState<Moving>();
  const siteId = sites.data?.some((site) => site.id === chosenSite) ? chosenSite : sites.data?.[0]?.id;
  const live = useScheduleLive(siteId);
  const schedule = useQuery({
    ...scheduleQueryOptions({ siteId: siteId ?? 0, from, days }, live),
    enabled: siteId !== undefined,
  });
  const manage = canManageOrders(meta.data);
  const shownFrom = schedule.data?.from ?? from;

  const chooseSite = (next: string | null) => {
    if (!next) return;
    setChosenSite(Number(next));
    rememberScheduleSite(Number(next));
  };

  return (
    <Stack gap="lg">
      <PageHeader breadcrumbs={breadcrumbs(t)} title={t("navSchedule")} description={t("scheduleDescription")} />
      <Group gap="sm" wrap="wrap">
        <Select
          aria-label={t("site")}
          placeholder={t("site")}
          allowDeselect={false}
          w={220}
          data={(sites.data ?? []).map((site) => ({ value: String(site.id), label: `${site.code} · ${site.name}` }))}
          value={siteId === undefined ? null : String(siteId)}
          onChange={chooseSite}
          data-assist="manufacturing.schedule.site"
        />
        <SegmentedControl
          aria-label={t("scheduleSpan")}
          value={String(days)}
          onChange={(next) => setDays(Number(next) as ScheduleSpan)}
          data={[
            { value: "7", label: t("scheduleWeek") },
            { value: "14", label: t("scheduleTwoWeeks") },
          ]}
        />
        <Group gap={4}>
          <Button
            variant="default"
            aria-label={t("schedulePrevious")}
            disabled={!shownFrom}
            onClick={() => shownFrom && setFrom(addDays(shownFrom, -days))}
          >
            <IconChevronLeft size={16} />
          </Button>
          <Button variant="default" onClick={() => setFrom(undefined)}>
            {t("scheduleToday")}
          </Button>
          <Button
            variant="default"
            aria-label={t("scheduleNext")}
            disabled={!shownFrom}
            onClick={() => shownFrom && setFrom(addDays(shownFrom, days))}
          >
            <IconChevronRight size={16} />
          </Button>
        </Group>
      </Group>
      {sites.data && sites.data.length === 0 ? (
        <EmptyState title={t("scheduleNoSites")} description={t("scheduleNoSitesHint")} />
      ) : schedule.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {refusalMessage(t, schedule.error)}
        </Alert>
      ) : !schedule.data || meta.isPending ? (
        <ContentSkeleton rows={4} rowHeight={ROW_HEIGHT} />
      ) : (
        <>
          <PageSection>
            {manage && (
              <Text size="xs" c="dimmed" mb="xs">
                {t("scheduleDragHint")}
              </Text>
            )}
            <Board schedule={schedule.data} canMove={manage} onOpen={(bar, centerId) => setMoving({ bar, centerId })} />
          </PageSection>
          <Unscheduled orders={schedule.data.unscheduled} siteId={schedule.data.siteId} canPlan={manage} />
        </>
      )}
      <MoveDialog
        key={moving ? `${moving.bar.workOrderId}-${moving.bar.startsAt}` : "none"}
        moving={moving}
        rows={schedule.data?.workCenters ?? []}
        onClose={() => setMoving(undefined)}
      />
    </Stack>
  );
};
