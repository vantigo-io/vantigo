import { Loader, Switch } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import {
  isVersionConflict,
  personQueryKey,
  personQueryOptions,
  putPerson,
  settingsQueryOptions,
} from "../api/assistant";
import "../i18n";

export interface AssistantPersonSwitchProps {
  /** The person the Owner is looking at. */
  userId: string;
}

/**
 * The per-person switch (S8) for a user's page in Workspace admin › Users. It
 * saves on its own, under the version it read (a person with no row yet is
 * version 0), and says plainly that it only counts in Per person mode.
 */
export const AssistantPersonSwitch = ({ userId }: AssistantPersonSwitchProps) => {
  const { t } = useI18n("assistant");
  const queryClient = useQueryClient();
  const person = useQuery(personQueryOptions(userId));
  const settings = useQuery(settingsQueryOptions());

  const save = useMutation({
    mutationFn: (enabled: boolean) => putPerson(userId, enabled, person.data?.version ?? 0),
    onSuccess: (saved) => queryClient.setQueryData(personQueryKey(userId), saved),
    onError: (error) => {
      if (isVersionConflict(error)) {
        notifications.show({ color: "yellow", title: t("staleTitle"), message: t("staleBody") });
        void queryClient.invalidateQueries({ queryKey: personQueryKey(userId) });
        return;
      }
      notifications.show({
        color: "red",
        title: t("personFailed"),
        message: error instanceof Error ? error.message : t("requestFailed"),
      });
    },
  });

  if (person.isPending) return <Loader size="xs" />;
  if (person.isError || !person.data) return null;

  return (
    <Switch
      label={t("personSwitch")}
      description={settings.data?.mode === "per_person" ? t("personHintPerPerson") : t("personHintOther")}
      checked={person.data.enabled}
      disabled={save.isPending}
      onChange={(event) => save.mutate(event.currentTarget.checked)}
    />
  );
};
