import { Alert, Group, Progress, Stack, Text } from "@mantine/core";
import type { Editor } from "@tiptap/core";
import { useI18n } from "@vantigo/frontend-shell";
import { useSyncExternalStore } from "react";

/**
 * The uploads under way in the editor (knowledge design K19), each with how
 * far it got, and the ones that failed with why, until dismissed.
 */
export const UploadList = ({ editor }: { editor: Editor }) => {
  const { t } = useI18n("knowledge");
  const store = editor.storage.knowledgeUploads.store;
  const uploads = useSyncExternalStore(store.subscribe, store.get);
  if (uploads.length === 0) return null;
  return (
    <Stack gap="xs" mb="xs">
      {uploads.map((upload) =>
        upload.problem ? (
          <Alert
            key={upload.id}
            color="red"
            role="alert"
            withCloseButton
            closeButtonLabel={t("close")}
            onClose={() => store.dismiss(upload.id)}
            title={t("uploadFailed", { name: upload.name })}
          >
            {t(`uploadProblem_${upload.problem}`)}
          </Alert>
        ) : (
          <Stack key={upload.id} gap={4} role="status" aria-label={t("uploading", { name: upload.name })}>
            <Group justify="space-between" gap="xs">
              <Text size="sm" truncate>
                {t("uploading", { name: upload.name })}
              </Text>
              <Text size="xs" c="dimmed">
                {Math.round(upload.progress * 100)} %
              </Text>
            </Group>
            <Progress value={upload.progress * 100} size="sm" aria-hidden />
          </Stack>
        ),
      )}
    </Stack>
  );
};
