import { ActionIcon, Button, Group, Menu, Modal, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconCheck,
  IconDots,
  IconHistory,
  IconLock,
  IconLockOpen,
  IconRobotOff,
  IconShare,
  IconStar,
  IconStarOff,
  IconTrash,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  addFavourite,
  articleQueryKey,
  favouritesQueryKey,
  favouritesQueryOptions,
  type KnowledgeArticle,
  type KnowledgeArticleUpdate,
  removeFavourite,
  trashArticle,
  treeQueryKey,
  updateArticle,
} from "../api/articles";
import { refusalCode } from "../api/request";
import { KNOWLEDGE_HOME_ROUTE_PATH } from "../lib/routes";

/**
 * The article's menu: share, history, favourite, the lock (K11a), Hidden from
 * the Assistant (K16) and the trash (K10). The writes are a writer's; a reader
 * sees share (read-only), history and favourite.
 */
export const PageMenu = ({
  article,
  onOpenAccess,
  onOpenHistory,
}: {
  article: KnowledgeArticle;
  onOpenAccess: () => void;
  onOpenHistory: () => void;
}) => {
  const { t } = useI18n("knowledge");
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [confirmTrash, setConfirmTrash] = useState(false);
  const favourites = useQuery(favouritesQueryOptions());
  const isFavourite = (favourites.data ?? []).some((node) => node.id === article.id);
  const canWrite = article.permission === "write";

  const patch = useMutation({
    mutationFn: (input: KnowledgeArticleUpdate) => updateArticle(article.id, input),
    onSuccess: (updated) => {
      queryClient.setQueryData(articleQueryKey(article.id), updated);
      void queryClient.invalidateQueries({ queryKey: treeQueryKey });
    },
    onError: (error) => {
      if (refusalCode(error) === "article_locked") {
        notifications.show({ color: "yellow", message: t("lockedNow") });
        void queryClient.invalidateQueries({ queryKey: articleQueryKey(article.id) });
      } else notifications.show({ color: "red", message: t("updateFailed") });
    },
  });

  const favourite = useMutation({
    mutationFn: () => (isFavourite ? removeFavourite(article.id) : addFavourite(article.id)),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: favouritesQueryKey }),
    onError: () => notifications.show({ color: "red", message: t("favouriteFailed") }),
  });

  const trash = useMutation({
    mutationFn: () => trashArticle(article.id),
    onSuccess: () => {
      setConfirmTrash(false);
      void queryClient.invalidateQueries({ queryKey: treeQueryKey });
      void queryClient.invalidateQueries({ queryKey: favouritesQueryKey });
      notifications.show({ color: "green", message: t("trashedNotice") });
      void navigate({ to: KNOWLEDGE_HOME_ROUTE_PATH });
    },
    onError: () => notifications.show({ color: "red", message: t("trashFailed") }),
  });

  const hidden = article.aiHidden || article.aiHiddenInherited;

  return (
    <>
      <Menu position="bottom-end" withinPortal>
        <Menu.Target>
          <ActionIcon variant="subtle" color="gray" aria-label={t("pageMenu")}>
            <IconDots size={18} />
          </ActionIcon>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Item leftSection={<IconShare size={14} />} onClick={onOpenAccess}>
            {t("share")}
          </Menu.Item>
          <Menu.Item leftSection={<IconHistory size={14} />} onClick={onOpenHistory}>
            {t("historyTitle")}
          </Menu.Item>
          <Menu.Item
            leftSection={isFavourite ? <IconStarOff size={14} /> : <IconStar size={14} />}
            onClick={() => favourite.mutate()}
          >
            {t(isFavourite ? "removeFavourite" : "addFavourite")}
          </Menu.Item>
          {canWrite && (
            <>
              <Menu.Divider />
              <Menu.Item
                leftSection={article.locked ? <IconLockOpen size={14} /> : <IconLock size={14} />}
                onClick={() => patch.mutate({ locked: !article.locked })}
              >
                {t(article.locked ? "unlockPage" : "lockPage")}
              </Menu.Item>
              <Menu.Item
                leftSection={<IconRobotOff size={14} />}
                rightSection={hidden ? <IconCheck size={14} aria-label={t("on")} /> : undefined}
                disabled={article.aiHiddenInherited}
                onClick={() => patch.mutate({ aiHidden: !article.aiHidden })}
              >
                <div>
                  <Text size="sm">{t("aiHidden")}</Text>
                  {article.aiHiddenInherited && (
                    <Text size="xs" c="dimmed">
                      {t("aiHiddenInherited")}
                    </Text>
                  )}
                </div>
              </Menu.Item>
              <Menu.Divider />
              <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={() => setConfirmTrash(true)}>
                {t("moveToTrash")}
              </Menu.Item>
            </>
          )}
        </Menu.Dropdown>
      </Menu>
      <Modal opened={confirmTrash} onClose={() => setConfirmTrash(false)} title={t("moveToTrashTitle")} centered>
        <Text size="sm">{t("moveToTrashBody")}</Text>
        <Group justify="flex-end" mt="lg">
          <Button variant="default" onClick={() => setConfirmTrash(false)}>
            {t("cancel")}
          </Button>
          <Button color="red" loading={trash.isPending} onClick={() => trash.mutate()}>
            {t("moveToTrash")}
          </Button>
        </Group>
      </Modal>
    </>
  );
};
