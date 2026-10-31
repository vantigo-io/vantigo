import { Alert, Badge, Button, Card, Group, Radio, Stack, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  invitationSettingsQueryKey,
  invitationSettingsQueryOptions,
  updateInvitationSettings,
} from "../api/invitation-settings";
import { ApiConflictError, ApiValidationError, CALENDAR_QUERY_KEY } from "../api/request";
import "../i18n";

/** The radio value of off: no channel, which the server stores as null. */
const OFF = "";

/**
 * Invitations to people outside the company — Sent from (integrations design
 * T6): the channel an Owner, or a holder of calendar:manage, chose in
 * Communications' channels, or off. Shown to calendar:manage holders alone; the
 * page asks the server only after the meta says the caller holds it. A channel
 * shows its provider; one that cannot send says so and is not offered unless it
 * is the stored choice. A stored channel that no longer exists reads as "Off —
 * the channel no longer exists" and is kept until someone chooses again.
 */
export const InvitationsCard = () => {
  const { t } = useI18n("calendar");
  const queryClient = useQueryClient();
  const query = useQuery(invitationSettingsQueryOptions());
  // The unsaved choice, undefined until the radio is touched.
  const [choice, setChoice] = useState<string | null | undefined>(undefined);
  const [fieldError, setFieldError] = useState<string | undefined>(undefined);
  const [conflict, setConflict] = useState(false);
  const settings = query.data;
  const save = useMutation({
    mutationFn: (invitationChannel: string | null) => {
      if (!settings) throw new Error("the invitation settings are not loaded");
      return updateInvitationSettings(settings.revision, invitationChannel);
    },
    onSuccess: async (saved) => {
      queryClient.setQueryData(invitationSettingsQueryKey, saved);
      setChoice(undefined);
      setFieldError(undefined);
      setConflict(false);
      // The meta says whether invitations are mailed; the choice moves it.
      await queryClient.invalidateQueries({ queryKey: [CALENDAR_QUERY_KEY, "meta"] });
      notifications.show({ color: "teal", title: t("settings.invitations.saved"), message: "" });
    },
    onError: (error: Error) => {
      if (error instanceof ApiConflictError) {
        setConflict(true);
        return;
      }
      if (error instanceof ApiValidationError && error.fieldErrors.invitationChannel) {
        setFieldError(t("settings.invitations.refused"));
        return;
      }
      notifications.show({ color: "red", title: t("settings.invitations.couldNotSave"), message: error.message });
    },
  });
  const reload = async () => {
    setChoice(undefined);
    setConflict(false);
    await queryClient.invalidateQueries({ queryKey: invitationSettingsQueryKey });
  };

  const channels = settings?.channels ?? [];
  const stored = settings?.invitationChannel ?? null;
  const chosen = choice === undefined ? stored : choice;
  // A choice no listed channel has: the stored one, gone.
  const gone = chosen !== null && !channels.some((channel) => channel.id === chosen);
  const selected = gone || chosen === null ? OFF : chosen;
  return (
    <Card withBorder component="section" aria-label={t("settings.invitations.title")}>
      <Stack>
        <Title order={4}>{t("settings.invitations.title")}</Title>
        {query.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {t("settings.invitations.couldNotLoad")}
          </Alert>
        )}
        {query.isPending && <ContentSkeleton rows={3} rowHeight={32} />}
        {conflict && (
          <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={t("settings.invitations.changedTitle")}>
            <Stack gap="xs" align="flex-start">
              <Text size="sm">{t("settings.invitations.changed")}</Text>
              <Button size="xs" variant="default" onClick={() => void reload()}>
                {t("settings.invitations.reload")}
              </Button>
            </Stack>
          </Alert>
        )}
        {settings && (
          <>
            <Radio.Group
              label={t("settings.invitations.sentFrom")}
              description={t("settings.invitations.description")}
              value={selected}
              error={fieldError}
              onChange={(value) => {
                setChoice(value === OFF ? null : value);
                setFieldError(undefined);
              }}
            >
              <Stack gap="xs" mt="xs">
                {channels.map((channel) => (
                  <Radio
                    key={channel.id}
                    value={channel.id}
                    disabled={!channel.available && channel.id !== chosen}
                    label={
                      <Group gap="xs">
                        <Text span fw={600}>
                          {channel.displayName || channel.address}
                        </Text>
                        {channel.displayName && (
                          <Text span size="sm" c="dimmed">
                            {channel.address}
                          </Text>
                        )}
                        <Badge variant="light">{channel.providerDisplayName}</Badge>
                        {!channel.available && (
                          <Badge color="orange" variant="light">
                            {t("settings.invitations.cannotSend")}
                          </Badge>
                        )}
                      </Group>
                    }
                  />
                ))}
                <Radio value={OFF} label={gone ? t("settings.invitations.gone") : t("settings.invitations.off")} />
              </Stack>
            </Radio.Group>
            {channels.length === 0 && (
              <Text size="sm" c="dimmed">
                {t("settings.invitations.noChannels")}
              </Text>
            )}
            <Group justify="flex-end">
              <Button
                loading={save.isPending}
                disabled={choice === undefined}
                onClick={() => save.mutate(choice ?? null)}
              >
                {t("save")}
              </Button>
            </Group>
          </>
        )}
      </Stack>
    </Card>
  );
};
