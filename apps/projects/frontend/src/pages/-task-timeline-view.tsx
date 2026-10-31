import { Badge, Box, Card, Group, ScrollArea, Stack, Text, Tooltip } from "@mantine/core";
import { IconAlertTriangle, IconDiamondFilled } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useMemo } from "react";
import { deliveryMilestonesQueryOptions } from "../api/delivery-milestones";
import { projectDependenciesQueryOptions } from "../api/dependencies";
import type { Task } from "../api/tasks";
import { DeliveryMilestoneBadge } from "../components/delivery-milestone-badge";
import { TaskStatusBadge } from "../components/task-status-badge";
import "../i18n";
import { useProjectDates } from "../lib/dates";

export interface TaskTimelineViewProps {
  projectId: number;
  tasks: Task[];
  canContribute: boolean;
  onOpen: (taskId: number) => void;
}

const DAY_WIDTH = 42; // Width of 1 day column in pixels
const ROW_HEIGHT = 44; // Height of each task row in pixels
const HEADER_HEIGHT = 50;

/** Parse YYYY-MM-DD into UTC epoch days */
const dateToUtcDays = (dateStr: string): number => {
  const [y, m, d] = dateStr.split("-").map(Number);
  return Math.floor(Date.UTC(y, m - 1, d) / (1000 * 60 * 60 * 24));
};

/** Convert UTC epoch days back to YYYY-MM-DD */
const utcDaysToDateStr = (days: number): string => {
  const date = new Date(days * 1000 * 60 * 60 * 24);
  return date.toISOString().split("T")[0];
};

export const TaskTimelineView = ({ projectId, tasks, onOpen }: TaskTimelineViewProps) => {
  const { t } = useI18n("projects");
  const dates = useProjectDates();

  const dependenciesQuery = useQuery(projectDependenciesQueryOptions(projectId));
  const milestonesQuery = useQuery(deliveryMilestonesQueryOptions(projectId));

  const dependencies = dependenciesQuery.data?.dependencies ?? [];
  const milestones = milestonesQuery.data?.milestones ?? [];

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

  const taskIndexMap = useMemo(() => {
    const map = new Map<number, { task: Task; index: number }>();
    flattenedTasks.forEach((t, i) => {
      map.set(t.id, { task: t, index: i });
    });
    return map;
  }, [flattenedTasks]);

  // Determine timeline date boundaries
  const todayUtcDays = useMemo(() => Math.floor(Date.now() / (1000 * 60 * 60 * 24)), []);

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

  // Days list for timeline columns
  const dayColumns = useMemo(() => {
    const cols: { daysSinceEpoch: number; dateStr: string; label: string; isToday: boolean }[] = [];
    for (let i = 0; i < totalDays; i++) {
      const dayEpoch = startEpochDays + i;
      const dateStr = utcDaysToDateStr(dayEpoch);
      const d = new Date(dayEpoch * 1000 * 60 * 60 * 24);
      const dayNum = d.getUTCDate();
      const monthShort = d.toLocaleString("en", { month: "short", timeZone: "UTC" });
      cols.push({
        daysSinceEpoch: dayEpoch,
        dateStr,
        label: `${dayNum} ${dayNum === 1 ? monthShort : ""}`.trim(),
        isToday: dayEpoch === todayUtcDays,
      });
    }
    return cols;
  }, [startEpochDays, totalDays, todayUtcDays]);

  const timelineWidth = totalDays * DAY_WIDTH;

  // Calculate task bar bounding box: { left, width, hasConflict }
  const taskCoordinates = useMemo(() => {
    const coords = new Map<number, { left: number; width: number; rowIndex: number; hasConflict: boolean }>();

    flattenedTasks.forEach((task, idx) => {
      let startD = task.startDate ? dateToUtcDays(task.startDate) : null;
      let endD = task.dueDate ? dateToUtcDays(task.dueDate) : null;

      if (!startD && !endD) return;

      if (startD && !endD) endD = startD;
      if (!startD && endD) startD = endD;

      if (startD !== null && endD !== null) {
        const left = (startD - startEpochDays) * DAY_WIDTH;
        const width = Math.max(DAY_WIDTH, (endD - startD + 1) * DAY_WIDTH);
        coords.set(task.id, { left, width, rowIndex: idx, hasConflict: false });
      }
    });

    return coords;
  }, [flattenedTasks, startEpochDays]);

  // Detect conflicts and build dependency connector paths
  const dependencyLinks = useMemo(() => {
    const links: {
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

      const hasConflict = Boolean(pred.task.dueDate && succ.task.startDate && pred.task.dueDate > succ.task.startDate);

      if (predCoords && succCoords) {
        // Connector starts at middle-right of predecessor bar
        const startX = predCoords.left + predCoords.width;
        const startY = predCoords.rowIndex * ROW_HEIGHT + ROW_HEIGHT / 2;

        // Connector ends at middle-left of successor bar
        const endX = succCoords.left;
        const endY = succCoords.rowIndex * ROW_HEIGHT + ROW_HEIGHT / 2;

        let path = "";
        if (endX >= startX + 10) {
          // Normal S-curve bezier or stepped line
          const midX = (startX + endX) / 2;
          path = `M ${startX} ${startY} C ${midX} ${startY}, ${midX} ${endY}, ${endX} ${endY}`;
        } else {
          // Loop-around backward connector (indicates conflict / overlap)
          const loopX = Math.max(startX + 14, startX);
          path = `M ${startX} ${startY} L ${loopX} ${startY} L ${loopX} ${endY} L ${endX} ${endY}`;
        }

        links.push({
          id: dep.id,
          path,
          hasConflict,
          predecessorId: pred.task.id,
          successorId: succ.task.id,
        });
      }
    }

    return links;
  }, [dependencies, taskIndexMap, taskCoordinates]);

  return (
    <Stack gap="sm">
      {/* Legend */}
      <Group justify="space-between" align="center" px="xs">
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
              {/* Timeline Header Row (Days/Dates) */}
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
                {dayColumns.map((col) => (
                  <Box
                    key={col.daysSinceEpoch}
                    style={{
                      width: DAY_WIDTH,
                      flexShrink: 0,
                      borderRight: "1px solid var(--mantine-color-gray-2)",
                      display: "flex",
                      flexDirection: "column",
                      alignItems: "center",
                      justifyContent: "center",
                      backgroundColor: col.isToday ? "var(--mantine-color-blue-0)" : undefined,
                    }}
                  >
                    <Text size="10px" fw={col.isToday ? 700 : 500} c={col.isToday ? "blue" : "dimmed"}>
                      {col.label}
                    </Text>
                  </Box>
                ))}
              </Box>

              {/* Delivery Milestones Markers Row */}
              {milestones.map((m) => {
                if (!m.targetDate) return null;
                const mDays = dateToUtcDays(m.targetDate);
                const leftPos = (mDays - startEpochDays) * DAY_WIDTH + DAY_WIDTH / 2 - 8;
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
                {dayColumns.map((col, idx) => (
                  <Box
                    key={col.daysSinceEpoch}
                    style={{
                      position: "absolute",
                      left: idx * DAY_WIDTH,
                      top: 0,
                      width: DAY_WIDTH,
                      height: "100%",
                      borderRight: "1px solid var(--mantine-color-gray-1)",
                      backgroundColor: col.isToday ? "rgba(34, 139, 230, 0.04)" : "transparent",
                    }}
                  />
                ))}

                {/* Today Vertical Line Indicator */}
                {todayUtcDays >= startEpochDays && todayUtcDays < startEpochDays + totalDays && (
                  <Box
                    style={{
                      position: "absolute",
                      left: (todayUtcDays - startEpochDays) * DAY_WIDTH + DAY_WIDTH / 2,
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

                const isDone = task.status === "done";
                const isInProgress = task.status === "in-progress";

                let barBg = "var(--mantine-color-gray-4)";
                if (isDone) barBg = "var(--mantine-color-teal-5)";
                else if (isInProgress) barBg = "var(--mantine-color-blue-5)";

                const hasConflict = dependencyLinks.some(
                  (l) => l.hasConflict && (l.predecessorId === task.id || l.successorId === task.id),
                );

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
                      <Tooltip
                        label={`${task.title} (${dates.range(task.startDate, task.dueDate)})${
                          hasConflict ? ` — ${t("dependencyConflictWarning")}` : ""
                        }`}
                        withArrow
                      >
                        <Box
                          onClick={() => onOpen(task.id)}
                          style={{
                            position: "absolute",
                            left: coords.left,
                            top: 8,
                            width: coords.width,
                            height: 28,
                            backgroundColor: barBg,
                            borderRadius: 4,
                            cursor: "pointer",
                            display: "flex",
                            alignItems: "center",
                            padding: "0 8px",
                            boxShadow: "0 1px 2px rgba(0,0,0,0.1)",
                            border: hasConflict ? "2px solid var(--mantine-color-red-6)" : "none",
                            zIndex: 1,
                            overflow: "hidden",
                          }}
                        >
                          <Text size="11px" fw={600} c="white" truncate>
                            {task.title}
                          </Text>
                        </Box>
                      </Tooltip>
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
    </Stack>
  );
};
