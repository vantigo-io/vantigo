import { ActionIcon, Alert, Button, Checkbox, Group, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { addChecklistItem, checklistQueryOptions, deleteChecklistItem, updateChecklistItem } from "../api/tasks";
import "../i18n";
import { CHECKLIST_TEXT_MAX, excerptForLabel } from "../lib/tasks";

export interface TaskChecklistProps {
  taskId: number;
  canContribute: boolean;
}

/**
 * The task's checklist: what the tree's `done/total` counts are an aggregate
 * over. Read by anyone who can see the project; written by its members and
 * managers.
 */
export const TaskChecklist = ({ taskId, canContribute }: TaskChecklistProps) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const { data: items, isPending, isError, error } = useQuery(checklistQueryOptions(taskId));
  const [text, setText] = useState("");

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["projects"] });
  const reportFailure = (failure: Error) =>
    notifications.show({ color: "red", title: t("couldNotSaveChecklistItem"), message: failure.message });

  // Ticking and deleting share one mutation; adding has its own, so a tick
  // neither clears the half-typed item beside it nor spins its button.
  const change = useMutation({
    mutationFn: (action: () => Promise<unknown>) => action(),
    onSuccess: invalidate,
    onError: reportFailure,
  });
  const add = useMutation({
    mutationFn: () => addChecklistItem(taskId, { text: text.trim() }),
    onSuccess: () => {
      setText("");
      return invalidate();
    },
    onError: reportFailure,
  });

  const trimmed = text.trim();
  const tooLong = trimmed.length > CHECKLIST_TEXT_MAX;

  return (
    <Stack gap="xs">
      <Text fw={600} component="h4">
        {t("checklist")}
      </Text>
      {isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadChecklist")}>
          {error.message}
        </Alert>
      )}
      {isPending && <ContentSkeleton rows={2} rowHeight={28} />}
      {items && items.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noChecklistItems")}
        </Text>
      )}
      {(items ?? []).map((item) => (
        <Group key={item.id} gap="sm" wrap="nowrap" justify="space-between">
          <Checkbox
            label={item.text}
            disabled={!canContribute || change.isPending}
            checked={item.done}
            onChange={(event) => {
              // Read off the event now: the mutation runs the closure after
              // React has let go of it, when `currentTarget` is already null.
              const done = event.currentTarget.checked;
              change.mutate(() => updateChecklistItem(taskId, item.id, { done }));
            }}
          />
          {canContribute && (
            // Named after its own item: a screen reader's list of buttons on a
            // checklist of more than one item gets one name per item, not many
            // that all read "Delete the item".
            <ActionIcon
              variant="subtle"
              color="red"
              aria-label={t("deleteChecklistItemFor", { text: excerptForLabel(item.text) })}
              onClick={() => change.mutate(() => deleteChecklistItem(taskId, item.id))}
            >
              <IconTrash size={16} />
            </ActionIcon>
          )}
        </Group>
      ))}
      {canContribute && (
        <Group gap="xs" wrap="nowrap" align="start" mt="xs">
          <TextInput
            aria-label={t("checklistItem")}
            placeholder={t("checklistItem")}
            flex={1}
            value={text}
            error={tooLong ? t("checklistTextTooLong") : undefined}
            onChange={(event) => setText(event.currentTarget.value)}
          />
          <Button
            variant="light"
            leftSection={<IconPlus size={14} />}
            loading={add.isPending}
            disabled={!trimmed || tooLong}
            onClick={() => add.mutate()}
          >
            {t("addChecklistItem")}
          </Button>
        </Group>
      )}
    </Stack>
  );
};
