import { Alert, Anchor, Card, Select, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, useI18n, useShellLink } from "@vantigo/frontend-shell";
import { type MyTask, myTasksQueryOptions } from "../api/tasks";
import "../i18n";
import { useProjectDates } from "../lib/dates";
import { isTaskStatus, taskStatuses, taskStatusLabelKey, taskUrl } from "../lib/tasks";
import { useTaskSave } from "../lib/use-task-save";

/**
 * Every open task assigned to the caller, across the projects they can see
 * (design §8). A done task is not open and never appears, so the status
 * picker on a row is also how a task leaves the list.
 */
export const MyTasksPage = () => {
  const { t } = useI18n("projects");
  const { data, isPending, isError, error } = useQuery(myTasksQueryOptions());

  return (
    <Stack gap="lg">
      <PageHeader title={t("myTasks")} description={t("myTasksDescription")} />

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMyTasks")}>
              {error.message}
            </Alert>
          )}
          {isPending && <ContentSkeleton rows={5} rowHeight={48} />}

          {data && data.length === 0 && (
            <EmptyState title={t("nothingAssignedToYou")} description={t("nothingAssignedToYouDescription")} />
          )}

          {data && data.length > 0 && (
            <Table.ScrollContainer minWidth={760}>
              <Table striped highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("project")}</Table.Th>
                    <Table.Th>{t("taskTitle")}</Table.Th>
                    <Table.Th>{t("status")}</Table.Th>
                    <Table.Th>{t("dueDate")}</Table.Th>
                    <Table.Th>{t("estimate")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {data.map((task) => (
                    <MyTaskRow key={task.id} task={task} />
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </Stack>
      </Card>
    </Stack>
  );
};

const MyTaskRow = ({ task }: { task: MyTask }) => {
  const { t, formatters } = useI18n("projects");
  const dates = useProjectDates();
  const Link = useShellLink();
  const save = useTaskSave();
  const projectHref = `/projects/${task.projectId}`;
  const taskHref = taskUrl(task.projectId, task.id);

  const link = (to: string, label: string, props?: { ff?: string; fw?: number }) =>
    Link ? (
      <Anchor size="sm" {...props} renderRoot={(anchorProps) => <Link to={to} {...anchorProps} />}>
        {label}
      </Anchor>
    ) : (
      <Anchor href={to} size="sm" {...props}>
        {label}
      </Anchor>
    );

  return (
    <Table.Tr>
      <Table.Td>
        <Stack gap={0}>
          {link(projectHref, task.projectCode, { ff: "monospace", fw: 600 })}
          <Text size="xs" c="dimmed">
            {task.projectName}
          </Text>
        </Stack>
      </Table.Td>
      <Table.Td>{link(taskHref, task.title)}</Table.Td>
      <Table.Td>
        <Select
          aria-label={t("changeTaskStatus")}
          w={150}
          size="xs"
          allowDeselect={false}
          data={taskStatuses.map((status) => ({ value: status, label: t(taskStatusLabelKey(status)) }))}
          value={task.status}
          disabled={save.isPending}
          onChange={(value) => value && isTaskStatus(value) && save.mutate({ task, changes: { status: value } })}
        />
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
    </Table.Tr>
  );
};
