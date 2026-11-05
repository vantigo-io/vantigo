import { Alert, Button, Group, Text } from "@mantine/core";
import { IconRefresh } from "@tabler/icons-react";
import { useOrdersFormat } from "../lib/format";

/**
 * The document moved on since the page read it (409 `version_conflict`,
 * O26): somebody else saved or changed its state. The edits on screen are
 * not sent again; Reload reads the document anew and drops them.
 */
export const ConflictAlert = ({ onReload, reloading }: { onReload: () => void; reloading?: boolean }) => {
  const { t } = useOrdersFormat();
  return (
    <Alert color="orange" title={t("conflictTitle")} role="alert" data-testid="conflict-alert">
      <Group justify="space-between" wrap="wrap">
        <Text size="sm">{t("conflictBody")}</Text>
        <Button size="xs" color="orange" leftSection={<IconRefresh size={14} />} loading={reloading} onClick={onReload}>
          {t("reload")}
        </Button>
      </Group>
    </Alert>
  );
};
