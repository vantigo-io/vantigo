import { Stack, Switch, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { connectionQueryOptions, type GoogleConnection } from "../api/connection";
import { setPersonalSwitch } from "../api/personal";
import { GOOGLE_QUERY_KEY } from "../api/request";
import { peopleReasonText, refusalText } from "../lib/words";
import { CopyLine, OwnerOnly, SectionCard } from "./step-card";

const colours = { off: "dimmed", ready: "teal", consent_missing: "orange", unavailable: "orange" } as const;

/**
 * **Connection for people** (Google Workspace design G28, G29): what it does,
 * the switch — the Owner's; anyone else reads it — with a question before it
 * turns off while anyone is connected, since that closes every person's
 * connection; the status with its reason; the counts; and the redirect URI to
 * register on the Internal web client.
 */
export const PeopleConnectionCard = ({ connection, owner }: { connection: GoogleConnection; owner: boolean }) => {
  const { t } = useI18n("google");
  const queryClient = useQueryClient();
  const personal = connection.personalConnection;

  const save = useMutation({
    mutationFn: setPersonalSwitch,
    onSuccess: async (answer) => {
      queryClient.setQueryData(connectionQueryOptions().queryKey, answer);
      notifications.show({
        color: "teal",
        title: t(answer.personalConnection.enabled ? "people.switchedOn" : "people.switchedOff"),
        message: "",
      });
      await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("people.couldNotSwitch"), message: refusalText(error, t) }),
  });

  const toggle = (enabled: boolean) => {
    if (enabled || personal.connected + personal.reconnect === 0) {
      save.mutate(enabled);
      return;
    }
    modals.openConfirmModal({
      title: t("people.offTitle"),
      children: <Text size="sm">{t("people.offBody")}</Text>,
      labels: { confirm: t("people.offConfirm"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => save.mutate(false),
    });
  };

  return (
    <SectionCard title={t("people.title")}>
      <Text size="sm">{t("people.intro")}</Text>
      <Switch
        label={t("people.switch")}
        description={t("people.switchDescription")}
        checked={personal.enabled}
        disabled={!owner || save.isPending}
        onChange={(event) => toggle(event.currentTarget.checked)}
      />
      {!owner && <OwnerOnly />}
      <Stack gap={0} role="status">
        <Text size="sm" c={colours[personal.status]}>
          {t(`people.status_${personal.status}`)}
        </Text>
        {personal.status === "consent_missing" && (
          <Text size="sm" c="dimmed">
            {t("people.consentMissing")}
          </Text>
        )}
        {personal.status === "unavailable" && personal.reason && (
          <Text size="sm" c="dimmed">
            {peopleReasonText(personal.reason, t)}
          </Text>
        )}
        <Text size="sm" c="dimmed">
          {t("people.counts", { connected: personal.connected, reconnect: personal.reconnect })}
        </Text>
      </Stack>
      <CopyLine label={t("people.redirectUri")} value={personal.redirectUri} />
      <Text size="sm" c="dimmed">
        {t("people.redirectUriHint")}
      </Text>
    </SectionCard>
  );
};
