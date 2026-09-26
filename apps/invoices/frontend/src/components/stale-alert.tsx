import { Alert, Button, Group, Stack, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";

export interface StaleAlertProps {
  title: string;
  message: string;
  /** Said when the last Reload failed; the alert stays so it can be tried again. */
  reloadFailedMessage?: string;
  reloading: boolean;
  onReload: () => void;
}

/**
 * What a form says when what it edits was saved by someone else meanwhile — a
 * save refused as stale, or a newer revision seen while editing: nothing to fix
 * but look at the latest version, so it offers Reload, which drops the unsaved
 * edits. The draft editor and the settings form both say it.
 */
export const StaleAlert = ({ title, message, reloadFailedMessage, reloading, onReload }: StaleAlertProps) => {
  const { t } = useI18n("invoices");
  return (
    <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={title}>
      <Stack gap="xs">
        <Text size="sm">{message}</Text>
        {reloadFailedMessage && (
          <Text size="sm" c="red">
            {reloadFailedMessage}
          </Text>
        )}
        <Group justify="flex-end">
          <Button size="xs" variant="light" color="yellow" loading={reloading} onClick={onReload}>
            {t("reload")}
          </Button>
        </Group>
      </Stack>
    </Alert>
  );
};
