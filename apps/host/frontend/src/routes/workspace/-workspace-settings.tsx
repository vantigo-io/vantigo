import { Button, Card, Group, Select, Stack, Text, ThemeIcon, Title } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconClock } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { sessionQueryKey } from "../../api/auth";
import {
  fetchWorkspaceSettings,
  timeZoneOptions,
  updateWorkspaceSettings,
  workspaceSettingsQueryKey,
} from "../../api/workspace";

/** A field error the server put on businessTimeZone, else the request's own message. */
const refusalMessage = (error: unknown, fallback: string): string => {
  const fields = (error as { fields?: Record<string, string[]> }).fields;
  const onZone = fields?.businessTimeZone?.[0];
  if (onZone) return onZone;
  return error instanceof Error && error.message ? error.message : fallback;
};

/**
 * The workspace's business time zone (time zones design D3): the one
 * calendar every bookkeeping day in the installation is judged in. An Owner
 * sets it here and every module reads it; changing it moves "today" and
 * every day derived from an instant from the next request on, which is why
 * the save is confirmed out loud, and dates already written stay what they
 * were.
 */
export const WorkspaceSettingsCard = () => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: workspaceSettingsQueryKey, queryFn: fetchWorkspaceSettings });
  const [draft, setDraft] = useState<string>();
  const current = settings.data?.businessTimeZone ?? "";
  const zone = draft ?? current;
  const [fieldError, setFieldError] = useState<string>();

  const save = useMutation({
    mutationFn: () => updateWorkspaceSettings({ businessTimeZone: zone }),
    onSuccess: (saved) => {
      queryClient.setQueryData(workspaceSettingsQueryKey, saved);
      void queryClient.invalidateQueries({ queryKey: workspaceSettingsQueryKey });
      void queryClient.invalidateQueries({ queryKey: sessionQueryKey });
      setDraft(undefined);
      setFieldError(undefined);
      notifications.show({ title: t("admin.workspaceSaved"), message: t("admin.workspaceSavedBody"), color: "teal" });
    },
    onError: (error) => setFieldError(refusalMessage(error, t("common.requestFailed"))),
  });

  const submit = () => {
    if (zone === current) return;
    modals.openConfirmModal({
      title: t("admin.businessTimeZoneChangeTitle"),
      children: <Text size="sm">{t("admin.businessTimeZoneChangeConfirm", { from: current, to: zone })}</Text>,
      labels: { confirm: t("common.save"), cancel: t("common.cancel") },
      onConfirm: () => save.mutate(),
    });
  };

  return (
    <Card withBorder radius="md" padding="lg">
      <Group gap="sm" mb="sm">
        <ThemeIcon variant="light" size="lg">
          <IconClock size={20} />
        </ThemeIcon>
        <Title order={3} fz="h4">
          {t("admin.workspace")}
        </Title>
      </Group>
      <Text size="sm" c="dimmed" mb="lg">
        {t("admin.workspaceDescription")}
      </Text>
      <Stack>
        <Select
          label={t("admin.businessTimeZone")}
          description={t("admin.businessTimeZoneDescription")}
          searchable
          allowDeselect={false}
          disabled={settings.isPending || settings.isError}
          data={timeZoneOptions(current)}
          value={zone || null}
          error={fieldError}
          onChange={(value) => {
            setFieldError(undefined);
            setDraft(value ?? undefined);
          }}
        />
        <Button w="fit-content" loading={save.isPending} disabled={zone === current || !zone} onClick={submit}>
          {t("admin.saveWorkspace")}
        </Button>
      </Stack>
    </Card>
  );
};
