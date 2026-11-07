import { Alert, Badge, Button, Drawer, Group, Loader, Stack, Text, Title, UnstyledButton } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconArrowLeft } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import {
  type KnowledgeArticle,
  type KnowledgeSaved,
  type KnowledgeVersion,
  restoreVersion,
  versionQueryOptions,
  versionsQueryOptions,
} from "../api/articles";
import { isVersionConflict } from "../api/request";
import { ArticleEditor } from "../editor/article-editor";
import { useArticleEditor } from "../editor/use-article-editor";
import { useKnowledgeFormat } from "../lib/format";

/**
 * An article's history (knowledge design K9): its versions with author and
 * time, one shown read-only, and **Restore**, which saves it as a new
 * version over the one the page holds.
 */
export const HistoryDrawer = ({
  article,
  opened,
  onClose,
  canRestore,
  onRestored,
}: {
  article: KnowledgeArticle;
  opened: boolean;
  onClose: () => void;
  canRestore: boolean;
  onRestored: (saved: KnowledgeSaved) => void;
}) => {
  const { t, dateTime, actor } = useKnowledgeFormat();
  const queryClient = useQueryClient();
  const versions = useQuery({ ...versionsQueryOptions(article.id), enabled: opened });
  const [selected, setSelected] = useState<number | null>(null);
  const version = useQuery({ ...versionQueryOptions(article.id, selected ?? 0), enabled: opened && selected !== null });

  const restore = useMutation({
    mutationFn: (number: number) => restoreVersion(article.id, number, article.version),
    onSuccess: (saved) => {
      void queryClient.invalidateQueries({ queryKey: versionsQueryOptions(article.id).queryKey });
      notifications.show({ color: "green", message: t("historyRestored") });
      setSelected(null);
      onRestored(saved);
      onClose();
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        message: t(isVersionConflict(error) ? "historyRestoreConflict" : "historyRestoreFailed"),
      }),
  });

  const close = () => {
    setSelected(null);
    onClose();
  };

  return (
    <Drawer opened={opened} onClose={close} position="right" size="xl" title={t("historyTitle")}>
      {selected !== null ? (
        <Stack gap="sm">
          <Group justify="space-between">
            <Button
              variant="subtle"
              size="xs"
              leftSection={<IconArrowLeft size={14} />}
              onClick={() => setSelected(null)}
            >
              {t("historyBack")}
            </Button>
            {canRestore && version.data && !versions.data?.find((v) => v.version === selected)?.current && (
              <Button size="xs" loading={restore.isPending} onClick={() => restore.mutate(selected)}>
                {t("historyRestore")}
              </Button>
            )}
          </Group>
          {version.isPending ? (
            <Loader size="sm" />
          ) : version.isError || !version.data ? (
            <Alert color="red">{t("historyLoadFailed")}</Alert>
          ) : (
            <VersionView version={version.data} />
          )}
        </Stack>
      ) : versions.isPending ? (
        <Loader size="sm" />
      ) : versions.isError ? (
        <Alert color="red">{t("historyLoadFailed")}</Alert>
      ) : (
        <Stack gap={4} role="list" aria-label={t("historyTitle")}>
          {(versions.data ?? []).map((item) => (
            <UnstyledButton
              key={item.version}
              role="listitem"
              p="xs"
              style={{ borderRadius: "var(--mantine-radius-sm)" }}
              onClick={() => setSelected(item.version)}
            >
              <Group justify="space-between" wrap="nowrap">
                <div>
                  <Text size="sm" fw={500}>
                    {dateTime(item.createdAt)}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {actor(item.createdBy)} · {item.title || t("untitled")}
                  </Text>
                </div>
                {item.current && <Badge variant="light">{t("historyCurrent")}</Badge>}
              </Group>
            </UnstyledButton>
          ))}
        </Stack>
      )}
    </Drawer>
  );
};

const VersionView = ({ version }: { version: KnowledgeVersion }) => {
  const { t, dateTime, actor } = useKnowledgeFormat();
  const editor = useArticleEditor({ content: version.content, editable: false });
  return (
    <Stack gap="xs">
      <Title order={3}>{version.title || t("untitled")}</Title>
      <Text size="xs" c="dimmed">
        {t("historyVersionBy", { name: actor(version.createdBy), when: dateTime(version.createdAt) })}
      </Text>
      <ArticleEditor editor={editor} editable={false} label={t("historyVersionLabel")} />
    </Stack>
  );
};
