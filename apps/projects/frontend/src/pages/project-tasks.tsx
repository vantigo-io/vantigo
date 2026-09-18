import {
  ActionIcon,
  Alert,
  Anchor,
  Avatar,
  Box,
  Button,
  Card,
  Group,
  Menu,
  SegmentedControl,
  SimpleGrid,
  Stack,
  Table,
  Text,
} from "@mantine/core";
import { IconAlertCircle, IconChevronDown, IconChevronRight, IconDots, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { projectQueryOptions } from "../api/projects";
import { projectTasksQueryOptions, type Task } from "../api/tasks";
import { TaskStatusBadge } from "../components/task-status-badge";
import "../i18n";
import { useProjectDates } from "../lib/dates";
import { taskStatuses, taskStatusLabelKey } from "../lib/tasks";
import { useTaskSave } from "../lib/use-task-save";
import { TaskDrawer } from "./-task-drawer";
import { TaskFormModal, type TaskModalState } from "./-task-form-modal";

/** Which of the two shapes of the same tree the caller last looked at. */
const VIEW_STORAGE_KEY = "projects.tasks.view";

type TaskView = "list" | "board";

/**
 * The remembered view. Storage is a convenience, never a requirement: a
 * private window or blocked site data simply starts on the list.
 */
const readView = (): TaskView => {
  try {
    return localStorage.getItem(VIEW_STORAGE_KEY) === "board" ? "board" : "list";
  } catch {
    return "list";
  }
};

const rememberView = (view: TaskView) => {
  try {
    localStorage.setItem(VIEW_STORAGE_KEY, view);
  } catch {
    // Remembering the choice is not worth failing the click over.
  }
};

/** Every task of the tree, parents and subtasks alike — what a board column draws from. */
const flatten = (tasks: Task[]): Task[] => tasks.flatMap((task) => [task, ...(task.subtasks ?? [])]);

export interface ProjectTasksProps {
  projectId: number;
  /** Who is looking, so the drawer knows whose comments may be edited. The host owns the session. */
  currentUserId?: string;
}

/**
 * The Tasks tab (design §8): the project's tree as either a list grouped by
 * status or a board of three columns. Who may add, move and edit a task is
 * `capabilities.canContribute`, which the project has already answered, so the
 * tab waits for it and says so when it fails rather than quietly rendering a
 * board nobody may act on.
 */
export const ProjectTasks = ({ projectId, currentUserId }: ProjectTasksProps) => {
  const { t } = useI18n("projects");
  const project = useQuery(projectQueryOptions(projectId));
  const tasks = useQuery(projectTasksQueryOptions(projectId));
  const [view, setView] = useState<TaskView>(readView);
  const [openTaskId, setOpenTaskId] = useState<number | null>(null);
  const [modalState, setModalState] = useState<TaskModalState | null>(null);

  if (project.isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProject")} mt="md">
        {project.error.message}
      </Alert>
    );
  }
  if (project.isPending)
    return (
      <Box mt="md">
        <ContentSkeleton rows={4} rowHeight={48} />
      </Box>
    );

  const { canContribute, canManage } = project.data.capabilities;
  const tree = tasks.data ?? [];

  const chooseView = (value: string) => {
    const chosen: TaskView = value === "board" ? "board" : "list";
    setView(chosen);
    rememberView(chosen);
  };

  return (
    <>
      <Card withBorder padding="lg" radius="md" mt="md">
        <Stack gap="md">
          <Group justify="space-between" wrap="wrap">
            <SegmentedControl
              aria-label={t("taskView")}
              data={[
                { value: "list", label: t("taskViewList") },
                { value: "board", label: t("taskViewBoard") },
              ]}
              value={view}
              onChange={chooseView}
            />
            {canContribute && (
              <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setModalState({ mode: "create" })}>
                {t("addTask")}
              </Button>
            )}
          </Group>

          {tasks.isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadTasks")}>
              {tasks.error.message}
            </Alert>
          )}
          {tasks.isPending && <ContentSkeleton rows={4} rowHeight={48} />}
          {tasks.data && tree.length === 0 && <EmptyState title={t("noTasksYet")} size="sm" />}

          {tree.length > 0 &&
            (view === "list" ? (
              <TaskList tasks={tree} onOpen={setOpenTaskId} />
            ) : (
              <TaskBoard tasks={tree} canContribute={canContribute} onOpen={setOpenTaskId} />
            ))}
        </Stack>
      </Card>

      <TaskFormModal projectId={projectId} state={modalState} onClose={() => setModalState(null)} />
      <TaskDrawer
        projectId={projectId}
        taskId={openTaskId}
        opened={openTaskId !== null}
        onClose={() => setOpenTaskId(null)}
        canContribute={canContribute}
        canManage={canManage}
        currentUserId={currentUserId}
      />
    </>
  );
};

/** Three sections, one per status, each a table of the top-level tasks standing in it. */
const TaskList = ({ tasks, onOpen }: { tasks: Task[]; onOpen: (taskId: number) => void }) => {
  const { t } = useI18n("projects");
  return (
    <Stack gap="lg">
      {taskStatuses.map((status) => {
        const rows = tasks.filter((task) => task.status === status);
        return (
          <Stack key={status} gap="xs" data-testid={`task-group-${status}`}>
            <Group gap="xs">
              <TaskStatusBadge status={status} />
              <Text size="sm" c="dimmed">
                {rows.length}
              </Text>
            </Group>
            {rows.length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("noTasksInStatus")}
              </Text>
            ) : (
              <Table.ScrollContainer minWidth={760}>
                <Table striped highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("taskTitle")}</Table.Th>
                      <Table.Th>{t("assignee")}</Table.Th>
                      <Table.Th>{t("dueDate")}</Table.Th>
                      <Table.Th>{t("estimate")}</Table.Th>
                      <Table.Th>{t("checklist")}</Table.Th>
                      <Table.Th>{t("comments")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {rows.map((task) => (
                      <TaskRow key={task.id} task={task} onOpen={onOpen} />
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}
          </Stack>
        );
      })}
    </Stack>
  );
};

/** One top-level task, and — once unfolded — the subtasks it carries, whatever their own status. */
const TaskRow = ({ task, onOpen }: { task: Task; onOpen: (taskId: number) => void }) => {
  const { t } = useI18n("projects");
  const [unfolded, setUnfolded] = useState(false);
  const subtasks = task.subtasks ?? [];

  return (
    <>
      <Table.Tr style={{ cursor: "pointer" }} onClick={() => onOpen(task.id)}>
        <Table.Td>
          <Group gap="xs" wrap="nowrap">
            {subtasks.length > 0 ? (
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label={unfolded ? t("hideSubtasks") : t("showSubtasks")}
                onClick={(event) => {
                  event.stopPropagation();
                  setUnfolded((open) => !open);
                }}
              >
                {unfolded ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
              </ActionIcon>
            ) : (
              <Box w={22} />
            )}
            <TaskTitleButton task={task} onOpen={onOpen} />
          </Group>
        </Table.Td>
        <TaskCells task={task} />
      </Table.Tr>
      {unfolded &&
        subtasks.map((subtask) => (
          <Table.Tr key={subtask.id} style={{ cursor: "pointer" }} onClick={() => onOpen(subtask.id)}>
            <Table.Td>
              <Group gap="xs" wrap="nowrap" pl={34}>
                <TaskStatusBadge status={subtask.status} size="xs" />
                <TaskTitleButton task={subtask} onOpen={onOpen} />
              </Group>
            </Table.Td>
            <TaskCells task={subtask} />
          </Table.Tr>
        ))}
    </>
  );
};

const TaskTitleButton = ({ task, onOpen }: { task: Task; onOpen: (taskId: number) => void }) => (
  <Anchor component="button" type="button" size="sm" ta="left" onClick={() => onOpen(task.id)}>
    {task.title}
  </Anchor>
);

/** The five columns every row shares, whether it is a task or one of its subtasks. */
const TaskCells = ({ task }: { task: Task }) => {
  const { t, formatters } = useI18n("projects");
  const dates = useProjectDates();
  return (
    <>
      <Table.Td>
        <Text size="sm" c={task.assignee ? undefined : "dimmed"}>
          {task.assignee?.displayName ?? t("unassigned")}
        </Text>
      </Table.Td>
      <Table.Td>
        <Text size="sm">{task.dueDate ? dates.day(task.dueDate) : t("notAvailable")}</Text>
      </Table.Td>
      <Table.Td>
        <Text size="sm">
          {task.estimateHours === null || task.estimateHours === undefined
            ? t("notAvailable")
            : t("hours", { hours: formatters.formatNumber(task.estimateHours) })}
        </Text>
      </Table.Td>
      <Table.Td>
        <Text size="sm">
          {task.checklist.total === 0
            ? t("notAvailable")
            : t("checklistProgress", { done: task.checklist.done, total: task.checklist.total })}
        </Text>
      </Table.Td>
      <Table.Td>
        <Text size="sm">
          {task.commentCount === 0 ? t("notAvailable") : formatters.formatNumber(task.commentCount)}
        </Text>
      </Table.Td>
    </>
  );
};

/**
 * Three columns of cards, subtasks among them: a board answers "what is in
 * each state", and a subtask has a state of its own. Nothing is dragged —
 * the workspace carries no drag-and-drop library — so a card is moved from
 * its own menu, which a keyboard reaches as readily as a pointer.
 */
const TaskBoard = ({
  tasks,
  canContribute,
  onOpen,
}: {
  tasks: Task[];
  canContribute: boolean;
  onOpen: (taskId: number) => void;
}) => {
  const { t } = useI18n("projects");
  const all = flatten(tasks);
  return (
    <SimpleGrid cols={{ base: 1, md: 3 }} spacing="md" data-testid="task-board">
      {taskStatuses.map((status) => {
        const cards = all.filter((task) => task.status === status);
        return (
          <Stack key={status} gap="xs" data-testid={`task-column-${status}`}>
            <Group gap="xs">
              <TaskStatusBadge status={status} />
              <Text size="sm" c="dimmed">
                {cards.length}
              </Text>
            </Group>
            {cards.length === 0 && (
              <Text size="sm" c="dimmed">
                {t("noTasksInStatus")}
              </Text>
            )}
            {cards.map((task) => (
              <TaskCard key={task.id} task={task} canContribute={canContribute} onOpen={onOpen} />
            ))}
          </Stack>
        );
      })}
    </SimpleGrid>
  );
};

/** The first letters of a display name, for the one-glance assignee a card has room for. */
const initialsOf = (displayName: string): string =>
  displayName
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase() ?? "")
    .join("");

const TaskCard = ({
  task,
  canContribute,
  onOpen,
}: {
  task: Task;
  canContribute: boolean;
  onOpen: (taskId: number) => void;
}) => {
  const { t } = useI18n("projects");
  const dates = useProjectDates();
  const save = useTaskSave();

  return (
    <Card
      withBorder
      padding="sm"
      radius="md"
      data-testid={`task-card-${task.id}`}
      style={{ cursor: "pointer" }}
      onClick={() => onOpen(task.id)}
    >
      <Stack gap="xs">
        <Group justify="space-between" align="start" wrap="nowrap">
          <TaskTitleButton task={task} onOpen={onOpen} />
          {canContribute && (
            // The menu opens on the card, which opens the drawer: the click
            // stops here so choosing a status never opens the task as well.
            <Box onClick={(event) => event.stopPropagation()}>
              <Menu position="bottom-end" withinPortal>
                <Menu.Target>
                  <ActionIcon variant="subtle" size="sm" aria-label={t("taskActions")}>
                    <IconDots size={16} />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  {taskStatuses
                    .filter((status) => status !== task.status)
                    .map((status) => (
                      <Menu.Item key={status} onClick={() => save.mutate({ task, changes: { status } })}>
                        {t("moveToStatus", { status: t(taskStatusLabelKey(status)) })}
                      </Menu.Item>
                    ))}
                </Menu.Dropdown>
              </Menu>
            </Box>
          )}
        </Group>
        <Group gap="xs" wrap="nowrap">
          {task.assignee ? (
            <Avatar size="sm" radius="xl" color="blue">
              {initialsOf(task.assignee.displayName)}
            </Avatar>
          ) : (
            <Text size="xs" c="dimmed">
              {t("unassigned")}
            </Text>
          )}
          {task.dueDate && (
            <Text size="xs" c="dimmed">
              {dates.day(task.dueDate)}
            </Text>
          )}
          {task.checklist.total > 0 && (
            <Text size="xs" c="dimmed">
              {t("checklistProgress", { done: task.checklist.done, total: task.checklist.total })}
            </Text>
          )}
        </Group>
      </Stack>
    </Card>
  );
};
