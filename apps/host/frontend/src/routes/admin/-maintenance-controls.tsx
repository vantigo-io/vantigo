import { Button, Card, Stack, Switch, Text, Textarea, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { fetchSystemStatus, setMaintenance, systemStatusQueryKey } from "../../api/system-status";

export const MaintenanceControls = () => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const maintenance = useQuery({ queryKey: systemStatusQueryKey, queryFn: fetchSystemStatus });
  const [draft, setDraft] = useState<{ enabled: boolean; message: string }>();
  const enabled = draft?.enabled ?? maintenance.data?.maintenance ?? false;
  const message = draft?.message ?? maintenance.data?.message ?? "";
  const save = useMutation({
    mutationFn: () => setMaintenance({ enabled, message: message.trim() || null }),
    onSuccess: (status) => {
      queryClient.setQueryData(systemStatusQueryKey, status);
      void queryClient.invalidateQueries({ queryKey: systemStatusQueryKey });
      notifications.show({
        title: t("systemAdmin.maintenanceSaved"),
        message: t("systemAdmin.maintenanceSavedBody"),
        color: "teal",
      });
    },
    onError: (error) =>
      notifications.show({
        title: t("systemAdmin.maintenanceSaveFailed"),
        message: error instanceof Error ? error.message : t("common.requestFailed"),
        color: "red",
      }),
  });
  return (
    <Card withBorder>
      <Stack>
        <div>
          <Title order={3}>{t("systemAdmin.maintenanceMode")}</Title>
          <Text size="sm" c="dimmed">
            {t("systemAdmin.maintenanceModeDescription")}
          </Text>
        </div>
        <Switch
          label={t("systemAdmin.maintenanceEnabled")}
          checked={enabled}
          onChange={(event) => setDraft({ enabled: event.currentTarget.checked, message })}
        />
        <Textarea
          label={t("systemAdmin.maintenanceMessage")}
          maxLength={500}
          value={message}
          onChange={(event) => setDraft({ enabled, message: event.currentTarget.value })}
        />
        <Button w="fit-content" loading={save.isPending} onClick={() => save.mutate()}>
          {t("systemAdmin.saveMaintenance")}
        </Button>
      </Stack>
    </Card>
  );
};
