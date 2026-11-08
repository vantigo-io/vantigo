import { Alert, Button, Group, Loader, NumberInput, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { putSettings, settingsQueryKey, settingsQueryOptions } from "../api/agents";
import { isVersionConflict } from "../api/request";
import type { AgentsSettings } from "../api/types";
import { RETENTION_MAX, RETENTION_MIN } from "../lib/modes";
import { useErrorText } from "./use-error-text";

/**
 * The company name and the retention (V11), saved with the version they were
 * read at: a stale save reloads the settings and says so.
 */
const Form = ({ settings, canManage }: { settings: AgentsSettings; canManage: boolean }) => {
  const { t } = useI18n("agents");
  const errorText = useErrorText();
  const queryClient = useQueryClient();
  const [companyName, setCompanyName] = useState(settings.companyName);
  const [retention, setRetention] = useState<number | string>(settings.retentionMonths);

  const retentionNumber = typeof retention === "number" ? retention : Number.NaN;
  const valid =
    companyName.trim() !== "" &&
    Number.isInteger(retentionNumber) &&
    retentionNumber >= RETENTION_MIN &&
    retentionNumber <= RETENTION_MAX;

  const save = useMutation({
    mutationFn: () =>
      putSettings({ companyName: companyName.trim(), retentionMonths: retentionNumber }, settings.version),
    onSuccess: (saved) => {
      queryClient.setQueryData(settingsQueryKey, saved);
      notifications.show({ color: "teal", title: t("saved"), message: "" });
    },
    onError: (error) => {
      if (isVersionConflict(error)) {
        notifications.show({ color: "yellow", title: t("staleTitle"), message: t("staleBody") });
        void queryClient.invalidateQueries({ queryKey: settingsQueryKey });
        return;
      }
      notifications.show({ color: "red", title: t("saveFailed"), message: errorText(error) });
    },
  });

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (valid && canManage) save.mutate();
      }}
    >
      <Stack gap="sm" maw={480}>
        <TextInput
          label={t("companyName")}
          description={t("companyNameHelp")}
          value={companyName}
          onChange={(event) => setCompanyName(event.currentTarget.value)}
          disabled={!canManage}
          required
        />
        <NumberInput
          label={t("retentionMonths")}
          description={t("retentionHelp")}
          value={retention}
          onChange={setRetention}
          min={RETENTION_MIN}
          max={RETENTION_MAX}
          allowDecimal={false}
          disabled={!canManage}
        />
        {canManage && (
          <Group>
            <Button type="submit" disabled={!valid} loading={save.isPending}>
              {t("save")}
            </Button>
          </Group>
        )}
      </Stack>
    </form>
  );
};

/** Loads the settings and shows their form; `key` restarts the form when a newer version arrives. */
export const SettingsForm = ({ canManage }: { canManage: boolean }) => {
  const { t } = useI18n("agents");
  const query = useQuery(settingsQueryOptions());
  if (query.isPending) return <Loader size="sm" />;
  if (query.isError || !query.data)
    return (
      <Alert color="red">
        <Group>
          <Text size="sm">{t("loadFailed")}</Text>
          <Button size="xs" variant="light" onClick={() => void query.refetch()}>
            {t("tryAgain")}
          </Button>
        </Group>
      </Alert>
    );
  return <Form key={query.data.version} settings={query.data} canManage={canManage} />;
};
