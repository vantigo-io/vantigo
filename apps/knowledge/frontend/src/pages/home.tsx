import { Button, Group, Modal, Radio, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconBook2, IconPlus } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { EmptyState, PageHeader, PageSection, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { createArticle, type KnowledgeRootSection, treeQueryKey } from "../api/articles";
import { KnowledgeLayout } from "../components/knowledge-layout";
import { ARTICLE_ROUTE_PATH, KNOWLEDGE_HOME_ROUTE_PATH } from "../lib/routes";

/**
 * The app's home (knowledge design K4): the sidebar's tree beside a start
 * page. `openNew` (the spotlight's New article, `?new=1`) opens the
 * new-article dialog on arrival.
 */
export const KnowledgeHomePage = ({ openNew = false }: { openNew?: boolean }) => {
  const { t } = useI18n("knowledge");
  const navigate = useNavigate();
  const [asked, setAsked] = useState(false);
  // The spotlight's New article lands here with `new=1`, also while the home is already open.
  const creating = asked || openNew;
  const close = () => {
    setAsked(false);
    if (openNew) void navigate({ to: KNOWLEDGE_HOME_ROUTE_PATH, search: {} as never, replace: true });
  };
  return (
    <KnowledgeLayout>
      <Stack gap="lg">
        <PageHeader
          title={t("homeTitle")}
          description={t("homeDescription")}
          actions={
            <Button leftSection={<IconPlus size={16} />} onClick={() => setAsked(true)}>
              {t("newArticle")}
            </Button>
          }
        />
        <PageSection>
          <EmptyState icon={IconBook2} title={t("homeEmptyTitle")} description={t("homeEmptyDescription")} />
        </PageSection>
      </Stack>
      <NewArticleDialog opened={creating} onClose={close} />
    </KnowledgeLayout>
  );
};

/** Where a new root goes (K4): the workspace, where everyone can edit it, or private, only the creator's. */
export const NewArticleDialog = ({ opened, onClose }: { opened: boolean; onClose: () => void }) => {
  const { t } = useI18n("knowledge");
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [section, setSection] = useState<KnowledgeRootSection>("workspace");
  const create = useMutation({
    mutationFn: () => createArticle({ section, title: "" }),
    onSuccess: (article) => {
      void queryClient.invalidateQueries({ queryKey: treeQueryKey });
      void navigate({ to: ARTICLE_ROUTE_PATH, params: { articleId: article.id } });
    },
    onError: () => notifications.show({ color: "red", message: t("createFailed") }),
  });
  return (
    <Modal opened={opened} onClose={onClose} title={t("newArticle")} centered>
      <Radio.Group
        label={t("newWhere")}
        value={section}
        onChange={(value) => setSection(value as KnowledgeRootSection)}
      >
        <Stack gap="xs" mt="xs">
          <Radio value="workspace" label={t("sectionWorkspace")} description={t("newWorkspaceHint")} />
          <Radio value="private" label={t("sectionPrivate")} description={t("newPrivateHint")} />
        </Stack>
      </Radio.Group>
      <Text size="xs" c="dimmed" mt="sm">
        {t("newChildHint")}
      </Text>
      <Group justify="flex-end" mt="lg">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button loading={create.isPending} onClick={() => create.mutate()}>
          {t("create")}
        </Button>
      </Group>
    </Modal>
  );
};
