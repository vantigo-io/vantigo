import { Alert, Button, Group, Modal, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconTrash, IconTrashOff } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  emptyTrash,
  favouritesQueryKey,
  type KnowledgeRestored,
  restoreArticle,
  trashQueryOptions,
  treeQueryKey,
} from "../api/articles";
import { ArticleIcon } from "../components/article-icon";
import { KnowledgeLayout } from "../components/knowledge-layout";
import type { KnowledgePageAccess } from "../lib/access";
import { useKnowledgeFormat } from "../lib/format";

/**
 * The trash (knowledge design K10): the trash groups the person could write,
 * each restored whole. knowledge:manage also empties it, after confirmation.
 */
export const TrashPage = ({ access }: { access: KnowledgePageAccess }) => {
  const { t, dateTime, date, actor } = useKnowledgeFormat();
  const queryClient = useQueryClient();
  const trash = useQuery(trashQueryOptions());
  const [confirmEmpty, setConfirmEmpty] = useState(false);
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: trashQueryOptions().queryKey });
    void queryClient.invalidateQueries({ queryKey: treeQueryKey });
    void queryClient.invalidateQueries({ queryKey: favouritesQueryKey });
  };

  const restore = useMutation({
    mutationFn: (id: string) => restoreArticle(id),
    onSuccess: (restored: KnowledgeRestored) => {
      refresh();
      notifications.show({
        color: "green",
        message: [
          t("restoredNotice", { count: restored.restoredCount }),
          restored.groupRestored ? t("restoredGroup") : "",
          restored.movedToRoot ? t("restoredToRoot") : "",
        ]
          .filter(Boolean)
          .join(" "),
      });
    },
    onError: () => notifications.show({ color: "red", message: t("restoreFailed") }),
  });

  const empty = useMutation({
    mutationFn: emptyTrash,
    onSuccess: () => {
      setConfirmEmpty(false);
      refresh();
      notifications.show({ color: "green", message: t("emptiedNotice") });
    },
    onError: () => notifications.show({ color: "red", message: t("emptyFailed") }),
  });

  const items = trash.data ?? [];

  return (
    <KnowledgeLayout>
      <Stack gap="lg">
        <PageHeader
          title={t("trashTitle")}
          description={t("trashDescription")}
          actions={
            access.canManage && items.length > 0 ? (
              <Button
                color="red"
                variant="light"
                leftSection={<IconTrash size={16} />}
                onClick={() => setConfirmEmpty(true)}
              >
                {t("emptyTrash")}
              </Button>
            ) : undefined
          }
        />
        <PageSection padding={items.length > 0 ? 0 : "lg"}>
          {trash.isPending ? (
            <ContentSkeleton rows={4} rowHeight={40} />
          ) : trash.isError ? (
            <Alert color="red">{t("trashLoadFailed")}</Alert>
          ) : items.length === 0 ? (
            <EmptyState icon={IconTrashOff} title={t("trashEmptyTitle")} description={t("trashEmptyDescription")} />
          ) : (
            <Table.ScrollContainer minWidth={640}>
              <Table verticalSpacing="sm" horizontalSpacing="md" aria-label={t("trashTitle")}>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("trashColumnArticle")}</Table.Th>
                    <Table.Th>{t("trashColumnTrashed")}</Table.Th>
                    <Table.Th>{t("trashColumnPurge")}</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {items.map((item) => (
                    <Table.Tr key={item.id}>
                      <Table.Td>
                        <Group gap={6} wrap="nowrap">
                          <ArticleIcon icon={item.icon} />
                          <div>
                            <Text size="sm" fw={500}>
                              {item.title || t("untitled")}
                            </Text>
                            <Text size="xs" c="dimmed">
                              {[
                                item.articleCount > 1 ? t("trashGroupCount", { count: item.articleCount - 1 }) : "",
                                item.parent ? t("trashParent", { title: item.parent.title || t("untitled") }) : "",
                              ]
                                .filter(Boolean)
                                .join(" · ")}
                            </Text>
                          </div>
                        </Group>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm">{dateTime(item.trashedAt)}</Text>
                        <Text size="xs" c="dimmed">
                          {actor(item.trashedBy)}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm">{date(item.purgeAt)}</Text>
                      </Table.Td>
                      <Table.Td ta="right">
                        <Button
                          size="xs"
                          variant="light"
                          loading={restore.isPending && restore.variables === item.id}
                          onClick={() => restore.mutate(item.id)}
                        >
                          {t("restore")}
                        </Button>
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </PageSection>
      </Stack>
      <Modal opened={confirmEmpty} onClose={() => setConfirmEmpty(false)} title={t("emptyTrashTitle")} centered>
        <Text size="sm">{t("emptyTrashBody")}</Text>
        <Group justify="flex-end" mt="lg">
          <Button variant="default" onClick={() => setConfirmEmpty(false)}>
            {t("cancel")}
          </Button>
          <Button color="red" loading={empty.isPending} onClick={() => empty.mutate()}>
            {t("emptyTrash")}
          </Button>
        </Group>
      </Modal>
    </KnowledgeLayout>
  );
};
