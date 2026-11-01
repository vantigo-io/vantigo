import {
  Badge,
  Box,
  Button,
  Card,
  Group,
  Modal,
  ScrollArea,
  SegmentedControl,
  Stack,
  Text,
  Tooltip,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconDiamondFilled } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type KeyboardEvent, type PointerEvent, useEffect, useMemo, useRef, useState } from "react";
import { type DeliveryMilestone, deliveryMilestonesQueryOptions } from "../api/delivery-milestones";
import { projectDependenciesQueryOptions, rescheduleDownstreamTasks, type TaskDependency } from "../api/dependencies";
import type { Task } from "../api/tasks";
import { DeliveryMilestoneBadge } from "../components/delivery-milestone-badge";
import { TaskStatusBadge } from "../components/task-status-badge";
import "../i18n";
import { useProjectDates } from "../lib/dates";
import {
  type DragMode,
  dateToUtcDays,
  downstreamTaskIds,
  dueDelta,
  headerSegments,
  isoWeek,
  isTimelineZoom,
  moveTaskDates,
  pixelsToDays,
  type TaskDates,
  type TimelineZoom,
  timelineZooms,
  utcDaysToDateStr,
  ZOOM_DAY_WIDTH,
} from "../lib/timeline";
import { useTaskSave } from "../lib/use-task-save";

export interface TaskTimelineViewProps {
  projectId: number;
  tasks: Task[];
  /** Whether the caller may move tasks (`capabilities.canContribute`); without it the timeline only shows. */
  canContribute: boolean;
  onOpen: (taskId: number) => void;
}

const ROW_HEIGHT = 44; // Height of each task row in pixels
const HEADER_HEIGHT = 50;
/** A bar narrower than this has no room for edge handles or a title, and is moved whole. */
const MIN_HANDLED_WIDTH = 24;
/** How far a pointer travels before a press is a drag rather than a click. */
const DRAG_THRESHOLD = 3;

// Stable fallbacks while the queries load, so the memos below do not rerun
// on every render.
const EMPTY_DEPENDENCIES: TaskDependency[] = [];
const EMPTY_MILESTONES: DeliveryMilestone[] = [];

/** The scale the viewer last chose, remembered beside the Tasks tab's view. */
const ZOOM_STORAGE_KEY = "projects.tasks.timelineZoom";

/** Storage is a convenience: a private window or blocked site data simply starts on days. */
const readZoom = (): TimelineZoom => {
  try {
    const stored = localStorage.getItem(ZOOM_STORAGE_KEY);
    return isTimelineZoom(stored) ? stored : "day";
  } catch {
    return "day";
  }
};

const rememberZoom = (zoom: TimelineZoom) => {
  try {
    localStorage.setItem(ZOOM_STORAGE_KEY, zoom);
  } catch {
    // Remembering the choice is not worth failing the click over.
  }
};

const zoomLabelKey: Record<TimelineZoom, string> = {
  day: "timelineZoomDay",
  week: "timelineZoomWeek",
  month: "timelineZoomMonth",
};

/** A bar's place on the chart, or null for a task with no date at all. */
const barOf = (dates: TaskDates, startEpochDays: number, dayWidth: number) => {
  const start = dates.startDate ? dateToUtcDays(dates.startDate) : null;
  const end = dates.dueDate ? dateToUtcDays(dates.dueDate) : null;
  const from = start ?? end;
  const to = end ?? start;
  if (from === null || to === null) return null;
  return { left: (from - startEpochDays) * dayWidth, width: Math.max(dayWidth, (to - from + 1) * dayWidth) };
};

/** The bar being dragged: which task, by which part, from where, and how many days so far. */
interface DragState {
  taskId: number;
  mode: DragMode;
  pointerId: number;
  originX: number;
  days: number;
  moved: boolean;
}

/** A saved move the task list has not caught up with yet, drawn in place of the stale dates. */
interface PendingMove extends TaskDates {
  taskId: number;
  revision: number;
}

/** The downstream shift the caller is asked about after a move changed a due date (M8). */
interface ShiftPrompt {
  taskId: number;
  count: number;
  days: number;
}

/**
 * The Tasks tab's timeline: one row per task, bars across calendar days,
 * delivery milestones as diamonds and finish-to-start links as arrows. A
 * contributor drags a bar's body to move both dates, an edge to move one, or
 * presses Alt+Left/Right on a focused bar; the move saves as any task edit
 * does, and a moved due date with successors asks before shifting them —
 * never silently (M8). Nothing here is a drag-and-drop library: the workspace
 * carries none, so it is pointer events with the pointer captured by the bar.
 */
export const TaskTimelineView = ({ projectId, tasks, canContribute, onOpen }: TaskTimelineViewProps) => {
  const { t, formatters } = useI18n("projects");
  const dates = useProjectDates();
  const queryClient = useQueryClient();
  const save = useTaskSave();

  const dependenciesQuery = useQuery(projectDependenciesQueryOptions(projectId));
  const milestonesQuery = useQuery(deliveryMilestonesQueryOptions(projectId));

  const dependencies = dependenciesQuery.data?.dependencies ?? EMPTY_DEPENDENCIES;
  const milestones = milestonesQuery.data?.milestones ?? EMPTY_MILESTONES;

  const [zoom, setZoom] = useState<TimelineZoom>(readZoom);
  const dayWidth = ZOOM_DAY_WIDTH[zoom];
  const [drag, setDrag] = useState<DragState | null>(null);
  const [pendingMove, setPendingMove] = useState<PendingMove | null>(null);
  const [shiftPrompt, setShiftPrompt] = useState<ShiftPrompt | null>(null);
  // The pointer handlers read the drag synchronously, between renders.
  const dragRef = useRef<DragState | null>(null);
  // A drag ends in a click on the bar; that click must not open the task.
  const suppressClickRef = useRef(false);

  const shift = useMutation({
    mutationFn: ({ taskId, days }: ShiftPrompt) => rescheduleDownstreamTasks(taskId, { daysDelta: days }),
    onSuccess: (result, { days }) => {
      queryClient.invalidateQueries({ queryKey: ["projects", "tasks"] });
      notifications.show({
        color: "teal",
        title: t("rescheduleDownstreamSuccess", { count: result.shiftedTasksCount, days }),
        message: "",
      });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: t("couldNotRescheduleDownstream"), message: error.message });
    },
  });

  // Escape abandons a drag: the bar goes back where it was and nothing is saved.
  useEffect(() => {
    if (!drag) return;
    const onKey = (event: globalThis.KeyboardEvent) => {
      if (event.key !== "Escape") return;
      dragRef.current = null;
      suppressClickRef.current = true;
      setDrag(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [drag]);

  // Flatten task tree (parents followed by subtasks)
  const flattenedTasks = useMemo(() => {
    const list: Task[] = [];
    for (const t of tasks) {
      list.push(t);
      if (t.subtasks?.length) {
        list.push(...t.subtasks);
      }
    }
    return list;
  }, [tasks]);

  /**
   * The dates each task is drawn at: its own, or a saved move's while the
   * list still holds the revision the move was made from — so a dropped bar
   * stays where it was dropped instead of jumping back until the refetch.
   */
  const shownDates = (task: Task): TaskDates =>
    pendingMove && pendingMove.taskId === task.id && pendingMove.revision === task.revision ? pendingMove : task;

  /** A task waiting on its own save cannot be moved again: the next save would carry a stale revision. */
  const isBusy = (task: Task) =>
    save.isPending || Boolean(pendingMove && pendingMove.taskId === task.id && pendingMove.revision === task.revision);

  const taskIndexMap = useMemo(() => {
    const map = new Map<number, { task: Task; index: number }>();
    flattenedTasks.forEach((t, i) => {
      map.set(t.id, { task: t, index: i });
    });
    return map;
  }, [flattenedTasks]);

  // Determine timeline date boundaries
  // Today is the browser's calendar date, counted the way the task dates are
  // (days since the epoch at UTC midnight); Date.now() alone is the UTC day,
  // which just after midnight in Oslo is still yesterday.
  const todayUtcDays = useMemo(() => {
    const now = new Date();
    return Math.floor(Date.UTC(now.getFullYear(), now.getMonth(), now.getDate()) / (1000 * 60 * 60 * 24));
  }, []);

  const { startEpochDays, totalDays } = useMemo(() => {
    let minDays = todayUtcDays - 3;
    let maxDays = todayUtcDays + 21;

    for (const task of flattenedTasks) {
      if (task.startDate) {
        const d = dateToUtcDays(task.startDate);
        if (d < minDays) minDays = d;
        if (d > maxDays) maxDays = d;
      }
      if (task.dueDate) {
        const d = dateToUtcDays(task.dueDate);
        if (d < minDays) minDays = d;
        if (d > maxDays) maxDays = d;
      }
    }

    for (const m of milestones) {
      if (m.targetDate) {
        const d = dateToUtcDays(m.targetDate);
        if (d < minDays) minDays = d;
        if (d > maxDays) maxDays = d;
      }
    }

    // Add padding days
    minDays -= 4;
    maxDays += 7;

    const span = Math.max(14, maxDays - minDays + 1);
    return { startEpochDays: minDays, totalDays: span };
  }, [flattenedTasks, milestones, todayUtcDays]);

  // The header's cells at the chosen scale: a day, a week or a month each.
  const segments = useMemo(() => {
    return headerSegments(startEpochDays, totalDays, zoom).map((segment) => {
      const dateStr = utcDaysToDateStr(segment.firstDay);
      let label: string;
      if (zoom === "week") label = t("timelineWeekLabel", { week: isoWeek(segment.firstDay) });
      else if (zoom === "month")
        label = formatters.formatDate(dateStr, { month: "short", year: "numeric", timeZone: "UTC" });
      else {
        const dayNum = new Date(segment.firstDay * 1000 * 60 * 60 * 24).getUTCDate();
        label =
          dayNum === 1
            ? `${dayNum} ${formatters.formatDate(dateStr, { month: "short", timeZone: "UTC" })}`
            : `${dayNum}`;
      }
      return { ...segment, label, isToday: zoom === "day" && segment.firstDay === todayUtcDays };
    });
  }, [startEpochDays, totalDays, zoom, todayUtcDays, t, formatters]);

  const timelineWidth = totalDays * dayWidth;

  // Calculate task bar bounding box: { left, width, rowIndex }
  const taskCoordinates = new Map<number, { left: number; width: number; rowIndex: number }>();
  flattenedTasks.forEach((task, idx) => {
    const bar = barOf(shownDates(task), startEpochDays, dayWidth);
    if (bar) taskCoordinates.set(task.id, { ...bar, rowIndex: idx });
  });

  // Detect conflicts and build dependency connector paths
  const dependencyLinks: {
    id: number;
    path: string;
    hasConflict: boolean;
    predecessorId: number;
    successorId: number;
  }[] = [];

  for (const dep of dependencies) {
    const pred = taskIndexMap.get(dep.predecessorTaskId);
    const succ = taskIndexMap.get(dep.successorTaskId);
    if (!pred || !succ) continue;

    const predCoords = taskCoordinates.get(pred.task.id);
    const succCoords = taskCoordinates.get(succ.task.id);

    const predDue = shownDates(pred.task).dueDate;
    const succStart = shownDates(succ.task).startDate;
    const hasConflict = Boolean(predDue && succStart && predDue > succStart);

    if (predCoords && succCoords) {
      // Connector starts at middle-right of predecessor bar
      const startX = predCoords.left + predCoords.width;
      const startY = predCoords.rowIndex * ROW_HEIGHT + ROW_HEIGHT / 2;

      // Connector ends at middle-left of successor bar
      const endX = succCoords.left;
      const endY = succCoords.rowIndex * ROW_HEIGHT + ROW_HEIGHT / 2;

      let path: string;
      if (endX >= startX + 10) {
        // Normal S-curve bezier or stepped line
        const midX = (startX + endX) / 2;
        path = `M ${startX} ${startY} C ${midX} ${startY}, ${midX} ${endY}, ${endX} ${endY}`;
      } else {
        // Loop-around backward connector (indicates conflict / overlap)
        const loopX = Math.max(startX + 14, startX);
        path = `M ${startX} ${startY} L ${loopX} ${startY} L ${loopX} ${endY} L ${endX} ${endY}`;
      }

      dependencyLinks.push({
        id: dep.id,
        path,
        hasConflict,
        predecessorId: pred.task.id,
        successorId: succ.task.id,
      });
    }
  }

  const chooseZoom = (value: string) => {
    if (!isTimelineZoom(value)) return;
    setZoom(value);
    rememberZoom(value);
  };

  /**
   * Saves a moved task through the same full replace every task edit uses; a
   * 409 is worded by the save hook. A move that changed nothing saves nothing.
   */
  const commitMove = (task: Task, next: TaskDates) => {
    const startDate = next.startDate ?? null;
    const dueDate = next.dueDate ?? null;
    if (startDate === (task.startDate ?? null) && dueDate === (task.dueDate ?? null)) return;
    const days = dueDelta(task, next);
    const count = downstreamTaskIds(task.id, dependencies).size;
    setPendingMove({ taskId: task.id, revision: task.revision, startDate, dueDate });
    save.mutate(
      { task, changes: { startDate, dueDate } },
      {
        onSuccess: () => {
          if (days !== 0 && count > 0) setShiftPrompt({ taskId: task.id, count, days });
        },
        onError: () => setPendingMove(null),
      },
    );
  };

  const onBarPointerDown = (event: PointerEvent<HTMLDivElement>, task: Task) => {
    if (!canContribute || event.button !== 0 || isBusy(task)) return;
    const edge = (event.target as HTMLElement).dataset.edge;
    const mode: DragMode = edge === "start" || edge === "end" ? edge : "move";
    // jsdom, and some old browsers, have no pointer capture; the drag still
    // works while the pointer stays over the bar.
    if (typeof event.currentTarget.setPointerCapture === "function") {
      event.currentTarget.setPointerCapture(event.pointerId);
    }
    const state: DragState = {
      taskId: task.id,
      mode,
      pointerId: event.pointerId,
      originX: event.clientX,
      days: 0,
      moved: false,
    };
    dragRef.current = state;
    setDrag(state);
  };

  const onBarPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    const current = dragRef.current;
    if (!current || current.pointerId !== event.pointerId) return;
    const dx = event.clientX - current.originX;
    const days = pixelsToDays(dx, dayWidth);
    const moved = current.moved || Math.abs(dx) > DRAG_THRESHOLD;
    if (days === current.days && moved === current.moved) return;
    const next = { ...current, days, moved };
    dragRef.current = next;
    setDrag(next);
  };

  const onBarPointerUp = (event: PointerEvent<HTMLDivElement>, task: Task) => {
    const current = dragRef.current;
    if (!current || current.pointerId !== event.pointerId) return;
    dragRef.current = null;
    setDrag(null);
    if (current.moved) suppressClickRef.current = true;
    commitMove(task, moveTaskDates(task, current.mode, current.days));
  };

  const onBarPointerCancel = () => {
    dragRef.current = null;
    setDrag(null);
  };

  const onBarClick = (task: Task) => {
    if (suppressClickRef.current) {
      suppressClickRef.current = false;
      return;
    }
    onOpen(task.id);
  };

  // The keyboard's way to do what a drag does: Alt+Left/Right moves the
  // whole task a day; Enter or Space opens it, as a click does.
  const onBarKeyDown = (event: KeyboardEvent<HTMLDivElement>, task: Task) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      onOpen(task.id);
      return;
    }
    if (!canContribute || !event.altKey || (event.key !== "ArrowLeft" && event.key !== "ArrowRight")) return;
    event.preventDefault();
    if (isBusy(task)) return;
    commitMove(task, moveTaskDates(task, "move", event.key === "ArrowRight" ? 1 : -1));
  };

  const shiftLabel = (days: number) =>
    days > 0 ? t("timelineShiftLater", { count: days }) : t("timelineShiftEarlier", { count: -days });

  return (
    <Stack gap="sm">
      {/* Legend */}
      <Group justify="space-between" align="center" px="xs" wrap="wrap">
        <Group gap="md">
          <Group gap={6}>
            <Box
              style={{
                width: 14,
                height: 10,
                backgroundColor: "var(--mantine-color-blue-6)",
                borderRadius: 2,
              }}
            />
            <Text size="xs" c="dimmed">
              {t("timelineLegendTask")}
            </Text>
          </Group>
          <Group gap={6}>
            <IconDiamondFilled size={12} style={{ color: "var(--mantine-color-grape-6)" }} />
            <Text size="xs" c="dimmed">
              {t("timelineLegendMilestone")}
            </Text>
          </Group>
          {dependencyLinks.some((l) => l.hasConflict) && (
            <Group gap={6}>
              <Badge color="red" size="xs" variant="light" leftSection={<IconAlertTriangle size={10} />}>
                {t("timelineLegendConflict")}
              </Badge>
            </Group>
          )}
        </Group>
        <SegmentedControl
          size="xs"
          aria-label={t("timelineZoom")}
          data={timelineZooms.map((value) => ({ value, label: t(zoomLabelKey[value]) }))}
          value={zoom}
          onChange={chooseZoom}
        />
      </Group>

      {/* Main Timeline View Grid */}
      <Card withBorder padding={0} radius="md" style={{ overflow: "hidden" }}>
        <Box style={{ display: "flex", width: "100%", position: "relative" }}>
          {/* Left Column: Tasks list (fixed width) */}
          <Box
            style={{
              width: 280,
              flexShrink: 0,
              borderRight: "1px solid var(--mantine-color-gray-3)",
              backgroundColor: "var(--mantine-color-body)",
              zIndex: 3,
            }}
          >
            {/* Left Header */}
            <Box
              style={{
                height: HEADER_HEIGHT,
                borderBottom: "1px solid var(--mantine-color-gray-3)",
                padding: "10px 14px",
                display: "flex",
                alignItems: "center",
              }}
            >
              <Text fw={600} size="xs" c="dimmed">
                {t("tasks")} ({flattenedTasks.length})
              </Text>
            </Box>

            {/* Left Task Rows */}
            {flattenedTasks.map((task) => {
              const isSubtask = task.parentTaskId !== null && task.parentTaskId !== undefined;
              return (
                <Box
                  key={task.id}
                  onClick={() => onOpen(task.id)}
                  style={{
                    height: ROW_HEIGHT,
                    padding: `0 12px 0 ${isSubtask ? 28 : 12}px`,
                    display: "flex",
                    alignItems: "center",
                    gap: 8,
                    cursor: "pointer",
                    borderBottom: "1px solid var(--mantine-color-gray-2)",
                    "&:hover": { backgroundColor: "var(--mantine-color-gray-0)" },
                  }}
                >
                  <TaskStatusBadge status={task.status} size="xs" />
                  <Text size="xs" fw={500} truncate style={{ flex: 1, minWidth: 0 }}>
                    {task.title}
                  </Text>
                  {task.deliveryMilestone && <DeliveryMilestoneBadge milestone={task.deliveryMilestone} size="xs" />}
                </Box>
              );
            })}
          </Box>

          {/* Right Column: Scrollable Timeline Chart */}
          <ScrollArea style={{ flex: 1 }} type="always" offsetScrollbars>
            <Box
              style={{
                width: timelineWidth,
                position: "relative",
                minHeight: HEADER_HEIGHT + flattenedTasks.length * ROW_HEIGHT,
              }}
            >
              {/* Timeline Header Row (days, weeks or months) */}
              <Box
                style={{
                  height: HEADER_HEIGHT,
                  display: "flex",
                  borderBottom: "1px solid var(--mantine-color-gray-3)",
                  backgroundColor: "var(--mantine-color-gray-0)",
                  position: "sticky",
                  top: 0,
                  zIndex: 2,
                }}
              >
                {segments.map((segment) => (
                  <Box
                    key={segment.key}
                    style={{
                      width: segment.length * dayWidth,
                      flexShrink: 0,
                      borderRight: "1px solid var(--mantine-color-gray-2)",
                      display: "flex",
                      flexDirection: "column",
                      alignItems: "center",
                      justifyContent: "center",
                      overflow: "hidden",
                      backgroundColor: segment.isToday ? "var(--mantine-color-blue-0)" : undefined,
                    }}
                  >
                    <Text
                      size="10px"
                      fw={segment.isToday ? 700 : 500}
                      c={segment.isToday ? "blue" : "dimmed"}
                      style={{ whiteSpace: "nowrap" }}
                    >
                      {segment.label}
                    </Text>
                  </Box>
                ))}
              </Box>

              {/* Delivery Milestones Markers Row */}
              {milestones.map((m) => {
                if (!m.targetDate) return null;
                const mDays = dateToUtcDays(m.targetDate);
                const leftPos = (mDays - startEpochDays) * dayWidth + dayWidth / 2 - 8;
                if (leftPos < -20 || leftPos > timelineWidth + 20) return null;

                return (
                  <Tooltip
                    key={m.id}
                    label={`${m.name} (${dates.day(m.targetDate)}) — ${m.progressPercent}%`}
                    withArrow
                  >
                    <Box
                      style={{
                        position: "absolute",
                        top: 28,
                        left: leftPos,
                        zIndex: 4,
                        cursor: "pointer",
                      }}
                    >
                      <IconDiamondFilled
                        size={16}
                        style={{
                          color:
                            m.status === "completed" ? "var(--mantine-color-teal-6)" : "var(--mantine-color-grape-6)",
                          filter: "drop-shadow(0 1px 2px rgba(0,0,0,0.2))",
                        }}
                      />
                    </Box>
                  </Tooltip>
                );
              })}

              {/* Grid Background Lines */}
              <Box
                style={{
                  position: "absolute",
                  top: HEADER_HEIGHT,
                  left: 0,
                  width: timelineWidth,
                  height: flattenedTasks.length * ROW_HEIGHT,
                  pointerEvents: "none",
                }}
              >
                {segments.map((segment) => (
                  <Box
                    key={segment.key}
                    style={{
                      position: "absolute",
                      left: segment.offset * dayWidth,
                      top: 0,
                      width: segment.length * dayWidth,
                      height: "100%",
                      borderRight: "1px solid var(--mantine-color-gray-1)",
                      backgroundColor: segment.isToday ? "rgba(34, 139, 230, 0.04)" : "transparent",
                    }}
                  />
                ))}

                {/* Today Vertical Line Indicator */}
                {todayUtcDays >= startEpochDays && todayUtcDays < startEpochDays + totalDays && (
                  <Box
                    style={{
                      position: "absolute",
                      left: (todayUtcDays - startEpochDays) * dayWidth + dayWidth / 2,
                      top: -HEADER_HEIGHT,
                      width: 2,
                      height: HEADER_HEIGHT + flattenedTasks.length * ROW_HEIGHT,
                      backgroundColor: "var(--mantine-color-blue-5)",
                      zIndex: 3,
                    }}
                  />
                )}
              </Box>

              {/* SVG Connectors for Dependencies */}
              <svg
                role="img"
                aria-label={t("dependencies")}
                style={{
                  position: "absolute",
                  top: HEADER_HEIGHT,
                  left: 0,
                  width: timelineWidth,
                  height: flattenedTasks.length * ROW_HEIGHT,
                  pointerEvents: "none",
                  zIndex: 2,
                }}
              >
                <title>{t("dependencies")}</title>
                <defs>
                  <marker
                    id="arrow-normal"
                    viewBox="0 0 10 10"
                    refX="6"
                    refY="5"
                    markerWidth="6"
                    markerHeight="6"
                    orient="auto-start-reverse"
                  >
                    <path d="M 0 1 L 8 5 L 0 9 z" fill="var(--mantine-color-gray-6)" />
                  </marker>
                  <marker
                    id="arrow-conflict"
                    viewBox="0 0 10 10"
                    refX="6"
                    refY="5"
                    markerWidth="6"
                    markerHeight="6"
                    orient="auto-start-reverse"
                  >
                    <path d="M 0 1 L 8 5 L 0 9 z" fill="var(--mantine-color-red-6)" />
                  </marker>
                </defs>
                {dependencyLinks.map((link) => (
                  <path
                    key={link.id}
                    d={link.path}
                    fill="none"
                    stroke={link.hasConflict ? "var(--mantine-color-red-6)" : "var(--mantine-color-gray-5)"}
                    strokeWidth={link.hasConflict ? 2 : 1.5}
                    strokeDasharray={link.hasConflict ? "4 3" : undefined}
                    markerEnd={link.hasConflict ? "url(#arrow-conflict)" : "url(#arrow-normal)"}
                  />
                ))}
              </svg>

              {/* Task Tracks & Bars */}
              {flattenedTasks.map((task, idx) => {
                const coords = taskCoordinates.get(task.id);
                const topY = HEADER_HEIGHT + idx * ROW_HEIGHT;
                const shown = shownDates(task);

                const isDone = task.status === "done";
                const isInProgress = task.status === "in-progress";

                let barBg = "var(--mantine-color-gray-4)";
                if (isDone) barBg = "var(--mantine-color-teal-5)";
                else if (isInProgress) barBg = "var(--mantine-color-blue-5)";

                const hasConflict = dependencyLinks.some(
                  (l) => l.hasConflict && (l.predecessorId === task.id || l.successorId === task.id),
                );

                const dragging = drag?.taskId === task.id ? drag : null;
                const preview = dragging ? moveTaskDates(task, dragging.mode, dragging.days) : null;
                const ghost = preview ? barOf(preview, startEpochDays, dayWidth) : null;
                const roomy = coords ? coords.width >= MIN_HANDLED_WIDTH : false;
                const range = dates.range(shown.startDate, shown.dueDate);

                return (
                  <Box
                    key={task.id}
                    style={{
                      position: "absolute",
                      top: topY,
                      left: 0,
                      width: timelineWidth,
                      height: ROW_HEIGHT,
                      borderBottom: "1px solid var(--mantine-color-gray-2)",
                    }}
                  >
                    {coords ? (
                      <>
                        <Tooltip
                          label={`${task.title} (${range})${hasConflict ? ` — ${t("dependencyConflictWarning")}` : ""}`}
                          withArrow
                          disabled={drag !== null}
                        >
                          <Box
                            role="button"
                            tabIndex={0}
                            aria-label={t(canContribute ? "timelineBarMovableLabel" : "timelineBarLabel", {
                              title: task.title,
                              dates: range,
                            })}
                            data-testid={`timeline-bar-${task.id}`}
                            onClick={() => onBarClick(task)}
                            onKeyDown={(event) => onBarKeyDown(event, task)}
                            onPointerDown={(event) => onBarPointerDown(event, task)}
                            onPointerMove={onBarPointerMove}
                            onPointerUp={(event) => onBarPointerUp(event, task)}
                            onPointerCancel={onBarPointerCancel}
                            style={{
                              position: "absolute",
                              left: coords.left,
                              top: 8,
                              width: coords.width,
                              height: 28,
                              backgroundColor: barBg,
                              borderRadius: 4,
                              cursor: canContribute ? (dragging ? "grabbing" : "grab") : "pointer",
                              // A contributor's touch drags the bar rather than scrolling the chart.
                              touchAction: canContribute ? "none" : undefined,
                              display: "flex",
                              alignItems: "center",
                              padding: roomy ? "0 8px" : 0,
                              boxShadow: "0 1px 2px rgba(0,0,0,0.1)",
                              border: hasConflict ? "2px solid var(--mantine-color-red-6)" : "none",
                              opacity: dragging ? 0.4 : 1,
                              zIndex: 1,
                              overflow: "hidden",
                              userSelect: "none",
                            }}
                          >
                            {canContribute && roomy && (
                              <>
                                <Box
                                  data-edge="start"
                                  data-testid={`timeline-bar-${task.id}-start`}
                                  aria-hidden
                                  style={{
                                    position: "absolute",
                                    left: 0,
                                    top: 0,
                                    bottom: 0,
                                    width: 6,
                                    cursor: "ew-resize",
                                  }}
                                />
                                <Box
                                  data-edge="end"
                                  data-testid={`timeline-bar-${task.id}-end`}
                                  aria-hidden
                                  style={{
                                    position: "absolute",
                                    right: 0,
                                    top: 0,
                                    bottom: 0,
                                    width: 6,
                                    cursor: "ew-resize",
                                  }}
                                />
                              </>
                            )}
                            {roomy && (
                              <Text size="11px" fw={600} c="white" truncate style={{ pointerEvents: "none" }}>
                                {task.title}
                              </Text>
                            )}
                          </Box>
                        </Tooltip>
                        {ghost && preview && (
                          <>
                            <Box
                              aria-hidden
                              style={{
                                position: "absolute",
                                left: ghost.left,
                                top: 8,
                                width: ghost.width,
                                height: 28,
                                borderRadius: 4,
                                border: "2px dashed var(--mantine-color-blue-6)",
                                backgroundColor: "rgba(34, 139, 230, 0.15)",
                                pointerEvents: "none",
                                zIndex: 2,
                              }}
                            />
                            <Box
                              role="status"
                              style={{
                                position: "absolute",
                                left: ghost.left + ghost.width + 6,
                                top: 10,
                                padding: "2px 6px",
                                borderRadius: 4,
                                whiteSpace: "nowrap",
                                backgroundColor: "var(--mantine-color-dark-6)",
                                color: "var(--mantine-color-white)",
                                fontSize: 11,
                                pointerEvents: "none",
                                zIndex: 5,
                              }}
                            >
                              {dates.range(preview.startDate, preview.dueDate)}
                            </Box>
                          </>
                        )}
                      </>
                    ) : (
                      /* Unscheduled placeholder */
                      <Box
                        style={{
                          height: "100%",
                          display: "flex",
                          alignItems: "center",
                          paddingLeft: 12,
                        }}
                      >
                        <Text size="10px" c="dimmed" fs="italic">
                          {t("timelineUnscheduledTasks")}
                        </Text>
                      </Box>
                    )}
                  </Box>
                );
              })}
            </Box>
          </ScrollArea>
        </Box>
      </Card>

      <Modal opened={shiftPrompt !== null} onClose={() => setShiftPrompt(null)} title={t("timelineShiftTitle")}>
        {shiftPrompt && (
          <Stack gap="md">
            <Text size="sm">
              {t("timelineShiftPrompt", { count: shiftPrompt.count, shift: shiftLabel(shiftPrompt.days) })}
            </Text>
            <Group justify="flex-end" gap="xs">
              <Button variant="default" onClick={() => setShiftPrompt(null)}>
                {t("timelineShiftLeave")}
              </Button>
              <Button
                onClick={() => {
                  shift.mutate(shiftPrompt);
                  setShiftPrompt(null);
                }}
              >
                {t("timelineShiftConfirm")}
              </Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </Stack>
  );
};
