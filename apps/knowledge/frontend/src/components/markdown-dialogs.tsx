import { Alert, Anchor, Button, Checkbox, FileInput, Group, List, Modal, Radio, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconFileImport } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type KnowledgeArticle, type KnowledgeRootSection, treeQueryKey } from "../api/articles";
import { exportArticle, importMarkdown, type KnowledgeImportResult } from "../api/files";
import { refusalCode } from "../api/request";
import { ARTICLE_ROUTE_PATH } from "../lib/routes";

/**
 * Export → Markdown (knowledge design K23): the page as a `.md`, or with its
 * subpages the reader may read as a `.zip`, downloaded.
 */
export const ExportDialog = ({
  article,
  opened,
  onClose,
}: {
  article: Pick<KnowledgeArticle, "id" | "title">;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t } = useI18n("knowledge");
  const [subtree, setSubtree] = useState(false);
  const download = useMutation({
    mutationFn: () => exportArticle(article.id, { subtree, title: article.title }),
    onSuccess: (name) => {
      notifications.show({ color: "green", message: t("exportDone", { name }) });
      onClose();
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        message: t(refusalCode(error) === "export_too_large" ? "exportTooLarge" : "exportFailed"),
      }),
  });
  return (
    <Modal opened={opened} onClose={onClose} title={t("exportTitle")} centered>
      <Stack gap="md">
        <Text size="sm">{t("exportBody")}</Text>
        <Checkbox
          label={t("exportSubpages")}
          description={t("exportSubpagesHint")}
          checked={subtree}
          onChange={(event) => setSubtree(event.currentTarget.checked)}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={download.isPending} onClick={() => download.mutate()}>
            {t("exportDownload")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

type Target = "here" | KnowledgeRootSection;

/**
 * Import Markdown (K23): one `.md`, or a `.zip` of an Obsidian vault or a
 * Notion export, under this page (when the reader may write it) or at the top
 * of the Workspace or Private. What could not be carried over is listed.
 */
export const ImportDialog = ({
  article,
  opened,
  onClose,
}: {
  article: Pick<KnowledgeArticle, "id" | "permission">;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t } = useI18n("knowledge");
  const queryClient = useQueryClient();
  const canWriteHere = article.permission === "write";
  const [file, setFile] = useState<File | null>(null);
  const [target, setTarget] = useState<Target>(canWriteHere ? "here" : "workspace");
  const [result, setResult] = useState<KnowledgeImportResult | null>(null);
  const [problem, setProblem] = useState<string | null>(null);

  const close = () => {
    setFile(null);
    setResult(null);
    setProblem(null);
    onClose();
  };

  const run = useMutation({
    mutationFn: (chosen: File) =>
      importMarkdown(chosen, target === "here" ? { parentId: article.id } : { section: target }),
    onMutate: () => setProblem(null),
    onSuccess: (answer) => {
      setResult(answer);
      void queryClient.invalidateQueries({ queryKey: treeQueryKey });
      notifications.show({ color: "green", message: t("importDone", { count: answer.created.length }) });
    },
    onError: (error) => {
      const fields = (error as { errors?: Record<string, string[]> }).errors;
      const detail = fields ? Object.values(fields).flat().join(" ") : (error as Error).message;
      const code = refusalCode(error);
      const status = (error as { status?: number }).status;
      if (code === "file_too_large") setProblem(t("importTooLarge"));
      else if (code === "import_busy" || status === 429) setProblem(t("importBusy"));
      else setProblem(fields ? detail : t("importFailed"));
    },
  });

  return (
    <Modal opened={opened} onClose={close} title={t("importTitle")} centered>
      {result ? (
        <Stack gap="md">
          <Text size="sm">{t("importDone", { count: result.created.length })}</Text>
          {result.created.length > 0 && (
            <List size="sm" spacing={2}>
              {result.created.slice(0, 20).map((created) => (
                <List.Item key={created.id}>
                  <Anchor
                    size="sm"
                    onClick={close}
                    renderRoot={(props) => (
                      <Link to={ARTICLE_ROUTE_PATH} params={{ articleId: created.id }} {...props} />
                    )}
                  >
                    {created.title || t("untitled")}
                  </Anchor>
                </List.Item>
              ))}
            </List>
          )}
          {result.warnings.length > 0 && (
            <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("importWarnings")}>
              <List size="sm" spacing={2}>
                {result.warnings.map((warning) => (
                  <List.Item key={warning}>{warning}</List.Item>
                ))}
              </List>
            </Alert>
          )}
          <Group justify="flex-end">
            <Button onClick={close}>{t("close")}</Button>
          </Group>
        </Stack>
      ) : (
        <Stack gap="md">
          <Text size="sm">{t("importBody")}</Text>
          <FileInput
            label={t("importFile")}
            placeholder={t("importFilePlaceholder")}
            accept=".md,.markdown,.zip,text/markdown,application/zip"
            leftSection={<IconFileImport size={16} />}
            value={file}
            onChange={setFile}
            clearable
          />
          <Radio.Group label={t("importWhere")} value={target} onChange={(value) => setTarget(value as Target)}>
            <Stack gap={6} mt={4}>
              {canWriteHere && <Radio value="here" label={t("importUnderThis")} />}
              <Radio value="workspace" label={t("importInWorkspace")} />
              <Radio value="private" label={t("importInPrivate")} />
            </Stack>
          </Radio.Group>
          {problem && (
            <Alert color="red" role="alert" icon={<IconAlertTriangle size={16} />}>
              {problem}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={close}>
              {t("cancel")}
            </Button>
            <Button disabled={!file} loading={run.isPending} onClick={() => file && run.mutate(file)}>
              {t("importSubmit")}
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
};
