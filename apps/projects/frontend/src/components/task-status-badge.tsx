import { Badge, type MantineSize } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";
import { type TaskStatus, taskStatusColor, taskStatusLabelKey } from "../lib/tasks";

export interface TaskStatusBadgeProps {
  status: TaskStatus;
  size?: MantineSize;
}

/** A task's status, in the one colour and wording every task view uses for it. */
export const TaskStatusBadge = ({ status, size }: TaskStatusBadgeProps) => {
  const { t } = useI18n("projects");
  return (
    <Badge variant="light" color={taskStatusColor(status)} size={size}>
      {t(taskStatusLabelKey(status))}
    </Badge>
  );
};
