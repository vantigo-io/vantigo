import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { ApiValidationError } from "../api/projects";
import type { ApiError } from "../api/request";
import { type Task, type TaskUpdateInput, taskUpdateFrom, updateTask } from "../api/tasks";
import "../i18n";

/** What a full replace needs, which a tree task and a my-tasks row both carry. */
type Savable = Parameters<typeof taskUpdateFrom>[0] & Pick<Task, "id">;

/**
 * Saving a task from wherever it was changed — a board card's menu, the
 * drawer, a my-tasks row. An update is a full replace, so the caller passes
 * the task as it stands and only the fields it means to change.
 *
 * A 409 is the one error worth wording ourselves: the revision has moved on,
 * and the only way out is to read the task again.
 */
export const useTaskSave = (onValidationError?: (fieldErrors: Record<string, string>) => void) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ task, changes }: { task: Savable; changes: Partial<TaskUpdateInput> }) =>
      updateTask(task.id, taskUpdateFrom(task, changes)),
    onSuccess: (saved) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      notifications.show({ color: "teal", title: t("taskSaved"), message: saved.title });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError && onValidationError) {
        onValidationError(error.fieldErrors);
        return;
      }
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotSaveTask"),
        message: conflict ? t("taskChangedElsewhere") : error.message,
      });
    },
  });
};
