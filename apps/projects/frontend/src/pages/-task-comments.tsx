import { ActionIcon, Alert, Badge, Button, Group, Stack, Text, Textarea } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { addComment, commentsQueryOptions, deleteComment, type TaskComment, updateComment } from "../api/tasks";
import "../i18n";
import { COMMENT_BODY_MAX } from "../lib/tasks";

export interface TaskCommentsProps {
  taskId: number;
  canContribute: boolean;
  /** A manager may delete anybody's comment, not only their own. */
  canManage: boolean;
  /** Who is looking, so an author may edit and delete their own. The host owns the session. */
  currentUserId?: string;
}

/**
 * The task's own history — which is why a task writes nothing to the project
 * timeline. Comments come oldest first, so "load more" reaches forward in time
 * and the pages simply stack: page 1 stays where it is.
 */
export const TaskComments = ({ taskId, canContribute, canManage, currentUserId }: TaskCommentsProps) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const [pageCount, setPageCount] = useState(1);
  const [body, setBody] = useState("");
  // The last page is what says whether there is another; every earlier page is
  // rendered by its own child, which reads the very same query key.
  const { data: lastPage, isPending, isError, error } = useQuery(commentsQueryOptions(taskId, pageCount));

  const post = useMutation({
    mutationFn: () => addComment(taskId, { body: body.trim() }),
    onSuccess: async () => {
      setBody("");
      await queryClient.invalidateQueries({ queryKey: ["projects"] });
      // A comment that filled a page the drawer had not reached would otherwise
      // be invisible behind "Load more" — the caller's own comment, no less.
      const refreshed = queryClient.getQueryData(commentsQueryOptions(taskId, pageCount).queryKey);
      const pages = refreshed?.pagination.totalPages ?? pageCount;
      if (pages > pageCount) setPageCount(pages);
    },
    onError: (failure) => {
      notifications.show({ color: "red", title: t("couldNotSaveComment"), message: failure.message });
    },
  });

  const trimmed = body.trim();
  const tooLong = trimmed.length > COMMENT_BODY_MAX;

  return (
    <Stack gap="xs">
      <Text fw={600} component="h4">
        {t("comments")}
      </Text>
      {isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadComments")}>
          {error.message}
        </Alert>
      )}
      {isPending && <ContentSkeleton rows={2} rowHeight={40} />}
      {lastPage && lastPage.pagination.totalCount === 0 && (
        <Text size="sm" c="dimmed">
          {t("noComments")}
        </Text>
      )}
      {Array.from({ length: pageCount }, (_, index) => index + 1).map((page) => (
        <CommentPage
          key={page}
          taskId={taskId}
          page={page}
          canContribute={canContribute}
          canManage={canManage}
          currentUserId={currentUserId}
        />
      ))}
      {lastPage?.pagination.hasNextPage && (
        <Group justify="center">
          <Button variant="subtle" size="xs" onClick={() => setPageCount((count) => count + 1)}>
            {t("loadMoreComments")}
          </Button>
        </Group>
      )}
      {canContribute && (
        <Stack gap="xs" mt="xs">
          <Textarea
            aria-label={t("writeComment")}
            placeholder={t("writeComment")}
            rows={3}
            value={body}
            error={tooLong ? t("commentTooLong") : undefined}
            onChange={(event) => setBody(event.currentTarget.value)}
          />
          <Group justify="flex-end">
            <Button loading={post.isPending} disabled={!trimmed || tooLong} onClick={() => post.mutate()}>
              {t("postComment")}
            </Button>
          </Group>
        </Stack>
      )}
    </Stack>
  );
};

const CommentPage = ({
  taskId,
  page,
  canContribute,
  canManage,
  currentUserId,
}: TaskCommentsProps & { page: number }) => {
  const { data } = useQuery(commentsQueryOptions(taskId, page));
  return (
    <>
      {(data?.data ?? []).map((comment) => (
        <CommentRow
          key={comment.id}
          taskId={taskId}
          comment={comment}
          canContribute={canContribute}
          canManage={canManage}
          currentUserId={currentUserId}
        />
      ))}
    </>
  );
};

const CommentRow = ({
  taskId,
  comment,
  canContribute,
  canManage,
  currentUserId,
}: TaskCommentsProps & { comment: TaskComment }) => {
  const { t, formatters } = useI18n("projects");
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<string | null>(null);
  const isAuthor = currentUserId !== undefined && comment.author.userId === currentUserId;
  const canEdit = canContribute && isAuthor;
  const canDelete = canManage || canEdit;

  const write = useMutation({
    mutationFn: (action: () => Promise<unknown>) => action(),
    onSuccess: () => {
      setDraft(null);
      return queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
    onError: (failure) => {
      notifications.show({ color: "red", title: t("couldNotSaveComment"), message: failure.message });
    },
  });

  const confirmRemove = () =>
    modals.openConfirmModal({
      title: t("deleteCommentTitle"),
      children: <Text size="sm">{t("deleteCommentWarning")}</Text>,
      labels: { confirm: t("deleteTheComment"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => write.mutate(() => deleteComment(taskId, comment.id)),
    });

  const trimmed = (draft ?? "").trim();
  const tooLong = trimmed.length > COMMENT_BODY_MAX;

  return (
    <Stack gap={4}>
      <Group gap="xs" wrap="nowrap" justify="space-between">
        <Group gap="xs" wrap="nowrap">
          <Text size="sm" fw={600}>
            {comment.author.displayName}
          </Text>
          {!comment.author.active && (
            <Badge size="xs" variant="light" color="gray">
              {t("inactiveUser")}
            </Badge>
          )}
          <Text size="xs" c="dimmed">
            {formatters.formatDate(comment.createdAt, { dateStyle: "medium", timeStyle: "short" })}
          </Text>
          {comment.editedAt && (
            <Text size="xs" c="dimmed">
              {t("commentEdited")}
            </Text>
          )}
        </Group>
        <Group gap={4} wrap="nowrap">
          {canEdit && draft === null && (
            <ActionIcon variant="subtle" aria-label={t("editTheComment")} onClick={() => setDraft(comment.body)}>
              <IconPencil size={14} />
            </ActionIcon>
          )}
          {canDelete && (
            <ActionIcon variant="subtle" color="red" aria-label={t("deleteTheComment")} onClick={confirmRemove}>
              <IconTrash size={14} />
            </ActionIcon>
          )}
        </Group>
      </Group>
      {draft === null ? (
        <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
          {comment.body}
        </Text>
      ) : (
        <Stack gap="xs">
          <Textarea
            aria-label={t("editTheComment")}
            rows={3}
            value={draft}
            error={tooLong ? t("commentTooLong") : undefined}
            onChange={(event) => setDraft(event.currentTarget.value)}
          />
          <Group justify="flex-end" gap="xs">
            <Button variant="default" size="xs" onClick={() => setDraft(null)}>
              {t("cancel")}
            </Button>
            <Button
              size="xs"
              loading={write.isPending}
              disabled={!trimmed || tooLong}
              onClick={() => write.mutate(() => updateComment(taskId, comment.id, { body: trimmed }))}
            >
              {t("saveChanges")}
            </Button>
          </Group>
        </Stack>
      )}
    </Stack>
  );
};
