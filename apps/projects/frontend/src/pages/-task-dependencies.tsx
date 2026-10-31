import { ActionIcon, Badge, Button, Card, Group, NumberInput, Select, Stack, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconArrowsExchange, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useMemo, useState } from "react";
import {
  createTaskDependency,
  deleteTaskDependency,
  projectDependenciesQueryOptions,
  rescheduleDownstreamTasks,
} from "../api/dependencies";
import { ApiValidationError } from "../api/request";
import { projectTasksQueryOptions, type Task } from "../api/tasks";
import { TaskStatusBadge } from "../components/task-status-badge";
import "../i18n";
import { useProjectDates } from "../lib/dates";

interface TaskDependenciesProps {
  projectId: number;
  task: Task;
  canContribute: boolean;
}

export const TaskDependencies = ({ projectId, task, canContribute }: TaskDependenciesProps) => {
  const { t } = useI18n("projects");
  const dates = useProjectDates();
  const queryClient = useQueryClient();

  const dependenciesQuery = useQuery(projectDependenciesQueryOptions(projectId));
  const tasksQuery = useQuery(projectTasksQueryOptions(projectId));

  const [selectedPredecessorId, setSelectedPredecessorId] = useState<string | null>(null);
  const [selectedSuccessorId, setSelectedSuccessorId] = useState<string | null>(null);
  const [shiftDays, setShiftDays] = useState<number>(1);

  // Flatten all tasks in the project so subtasks can also be mapped
  const allTasks = useMemo(() => {
    const flat: Task[] = [];
    const traverse = (items: Task[]) => {
      for (const item of items) {
        flat.push(item);
        if (item.subtasks?.length) traverse(item.subtasks);
      }
    };
    if (tasksQuery.data) traverse(tasksQuery.data);
    return flat;
  }, [tasksQuery.data]);

  const taskMap = useMemo(() => new Map<number, Task>(allTasks.map((tk) => [tk.id, tk])), [allTasks]);

  const deps = dependenciesQuery.data?.dependencies ?? [];
  const predecessors = useMemo(() => deps.filter((d) => d.successorTaskId === task.id), [deps, task.id]);
  const successors = useMemo(() => deps.filter((d) => d.predecessorTaskId === task.id), [deps, task.id]);

  const addDependencyMutation = useMutation({
    mutationFn: ({ predId, succId }: { predId: number; succId: number }) =>
      createTaskDependency(projectId, { predecessorTaskId: predId, successorTaskId: succId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects", "dependencies"] });
      setSelectedPredecessorId(null);
      setSelectedSuccessorId(null);
      notifications.show({ color: "teal", title: t("dependencyAdded"), message: "" });
    },
    onError: (err: unknown) => {
      let msg = t("couldNotAddDependency");
      if (err instanceof ApiValidationError) {
        msg = err.errors?.cycle?.[0] || err.errors?.dependency?.[0] || err.message;
      } else if (err instanceof Error) {
        msg = err.message;
      }
      notifications.show({
        color: "red",
        title: t("couldNotAddDependency"),
        message: msg,
      });
    },
  });

  const removeDependencyMutation = useMutation({
    mutationFn: (depId: number) => deleteTaskDependency(depId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects", "dependencies"] });
      notifications.show({ color: "teal", title: t("dependencyDeleted"), message: "" });
    },
    onError: (err: Error) => {
      notifications.show({ color: "red", title: t("couldNotDeleteDependency"), message: err.message });
    },
  });

  const rescheduleMutation = useMutation({
    mutationFn: (days: number) => rescheduleDownstreamTasks(task.id, { daysDelta: days }),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ["projects", "tasks"] });
      notifications.show({
        color: "teal",
        title: t("rescheduleDownstreamSuccess", { count: res.shiftedTasksCount, days: shiftDays }),
        message: "",
      });
    },
    onError: (err: Error) => {
      notifications.show({ color: "red", title: t("couldNotRescheduleDownstream"), message: err.message });
    },
  });

  const predecessorOptions = allTasks
    .filter((tk) => tk.id !== task.id && !predecessors.some((p) => p.predecessorTaskId === tk.id))
    .map((tk) => ({ value: String(tk.id), label: tk.title }));

  const successorOptions = allTasks
    .filter((tk) => tk.id !== task.id && !successors.some((s) => s.successorTaskId === tk.id))
    .map((tk) => ({ value: String(tk.id), label: tk.title }));

  return (
    <Stack gap="md">
      <Group justify="space-between" align="center">
        <Title order={5}>{t("dependencies")}</Title>
      </Group>

      {/* Predecessors Section */}
      <Stack gap="xs">
        <Text fw={600} size="sm">
          {t("predecessors")}
        </Text>
        {predecessors.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noPredecessors")}
          </Text>
        ) : (
          <Stack gap="xs">
            {predecessors.map((dep) => {
              const predTask = taskMap.get(dep.predecessorTaskId);
              if (!predTask) return null;
              const hasConflict = predTask.dueDate && task.startDate && predTask.dueDate > task.startDate;

              return (
                <Card key={dep.id} withBorder padding="xs" radius="sm">
                  <Group justify="space-between" wrap="nowrap">
                    <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
                      <Group gap="xs" wrap="nowrap">
                        <TaskStatusBadge status={predTask.status} size="xs" />
                        <Text size="sm" fw={500} truncate>
                          {predTask.title}
                        </Text>
                        {hasConflict && (
                          <Badge color="red" size="xs" variant="light" leftSection={<IconAlertTriangle size={12} />}>
                            {t("dependencyConflictBadge")}
                          </Badge>
                        )}
                      </Group>
                      <Text size="xs" c="dimmed">
                        {dates.range(predTask.startDate, predTask.dueDate)}
                      </Text>
                      {hasConflict && (
                        <Text size="xs" c="red">
                          {t("dependencyConflictWarning")}
                        </Text>
                      )}
                    </Stack>
                    {canContribute && (
                      <ActionIcon
                        color="red"
                        variant="subtle"
                        size="sm"
                        aria-label={t("couldNotDeleteDependency")}
                        onClick={() => removeDependencyMutation.mutate(dep.id)}
                      >
                        <IconTrash size={14} />
                      </ActionIcon>
                    )}
                  </Group>
                </Card>
              );
            })}
          </Stack>
        )}

        {canContribute && predecessorOptions.length > 0 && (
          <Group gap="xs" mt={4}>
            <Select
              placeholder={t("selectTask")}
              data={predecessorOptions}
              value={selectedPredecessorId}
              onChange={setSelectedPredecessorId}
              size="xs"
              searchable
              style={{ flex: 1 }}
            />
            <Button
              size="xs"
              variant="light"
              leftSection={<IconPlus size={14} />}
              disabled={!selectedPredecessorId}
              loading={addDependencyMutation.isPending}
              onClick={() => {
                if (!selectedPredecessorId) return;
                addDependencyMutation.mutate({
                  predId: Number(selectedPredecessorId),
                  succId: task.id,
                });
              }}
            >
              {t("addPredecessor")}
            </Button>
          </Group>
        )}
      </Stack>

      {/* Successors Section */}
      <Stack gap="xs" mt="sm">
        <Text fw={600} size="sm">
          {t("successors")}
        </Text>
        {successors.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noSuccessors")}
          </Text>
        ) : (
          <Stack gap="xs">
            {successors.map((dep) => {
              const succTask = taskMap.get(dep.successorTaskId);
              if (!succTask) return null;
              const hasConflict = task.dueDate && succTask.startDate && task.dueDate > succTask.startDate;

              return (
                <Card key={dep.id} withBorder padding="xs" radius="sm">
                  <Group justify="space-between" wrap="nowrap">
                    <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
                      <Group gap="xs" wrap="nowrap">
                        <TaskStatusBadge status={succTask.status} size="xs" />
                        <Text size="sm" fw={500} truncate>
                          {succTask.title}
                        </Text>
                        {hasConflict && (
                          <Badge color="red" size="xs" variant="light" leftSection={<IconAlertTriangle size={12} />}>
                            {t("dependencyConflictBadge")}
                          </Badge>
                        )}
                      </Group>
                      <Text size="xs" c="dimmed">
                        {dates.range(succTask.startDate, succTask.dueDate)}
                      </Text>
                      {hasConflict && (
                        <Text size="xs" c="red">
                          {t("dependencyConflictWarning")}
                        </Text>
                      )}
                    </Stack>
                    {canContribute && (
                      <ActionIcon
                        color="red"
                        variant="subtle"
                        size="sm"
                        aria-label={t("couldNotDeleteDependency")}
                        onClick={() => removeDependencyMutation.mutate(dep.id)}
                      >
                        <IconTrash size={14} />
                      </ActionIcon>
                    )}
                  </Group>
                </Card>
              );
            })}
          </Stack>
        )}

        {canContribute && successorOptions.length > 0 && (
          <Group gap="xs" mt={4}>
            <Select
              placeholder={t("selectTask")}
              data={successorOptions}
              value={selectedSuccessorId}
              onChange={setSelectedSuccessorId}
              size="xs"
              searchable
              style={{ flex: 1 }}
            />
            <Button
              size="xs"
              variant="light"
              leftSection={<IconPlus size={14} />}
              disabled={!selectedSuccessorId}
              loading={addDependencyMutation.isPending}
              onClick={() => {
                if (!selectedSuccessorId) return;
                addDependencyMutation.mutate({
                  predId: task.id,
                  succId: Number(selectedSuccessorId),
                });
              }}
            >
              {t("addSuccessor")}
            </Button>
          </Group>
        )}
      </Stack>

      {/* Downstream Cascade Reschedule Section */}
      {canContribute && successors.length > 0 && (
        <Card withBorder padding="sm" radius="sm" mt="xs" bg="var(--mantine-color-gray-0)">
          <Stack gap="xs">
            <Text fw={600} size="xs">
              {t("rescheduleDownstream")}
            </Text>
            <Group gap="xs">
              <NumberInput
                size="xs"
                value={shiftDays}
                onChange={(val) => setShiftDays(typeof val === "number" ? val : 1)}
                min={-365}
                max={365}
                step={1}
                style={{ width: 90 }}
                aria-label={t("rescheduleDownstreamDays")}
              />
              <Button
                size="xs"
                variant="default"
                leftSection={<IconArrowsExchange size={14} />}
                loading={rescheduleMutation.isPending}
                onClick={() => rescheduleMutation.mutate(shiftDays)}
              >
                {t("rescheduleDownstreamAction")}
              </Button>
            </Group>
          </Stack>
        </Card>
      )}
    </Stack>
  );
};
