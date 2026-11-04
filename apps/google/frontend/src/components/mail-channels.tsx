import { Alert, Stack, Switch, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { connectionQueryOptions, type GoogleConnection, setMailChannels } from "../api/connection";
import { GOOGLE_QUERY_KEY } from "../api/request";
import { GMAIL_SCOPES } from "../lib/gmail";
import { mailReasonText, refusalText } from "../lib/words";
import { CopyLine, OwnerOnly, SectionCard } from "./step-card";

const colours = { off: "dimmed", ready: "teal", unavailable: "orange" } as const;

/**
 * **Mail channels** (Google Workspace design G33, G34): what it does, the
 * switch — the Owner's; anyone else reads it —, the warning that domain-wide
 * delegation reaches every mailbox in the domain, the two Gmail scopes to
 * delegate, the status with its reason, and how many mailboxes channels use.
 */
export const MailChannelsCard = ({ connection, owner }: { connection: GoogleConnection; owner: boolean }) => {
  const { t } = useI18n("google");
  const queryClient = useQueryClient();
  const mail = connection.mailChannels;

  const save = useMutation({
    mutationFn: setMailChannels,
    onSuccess: async (answer) => {
      queryClient.setQueryData(connectionQueryOptions().queryKey, answer);
      notifications.show({
        color: "teal",
        title: t(answer.mailChannels.enabled ? "mail.switchedOn" : "mail.switchedOff"),
        message: "",
      });
      await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("mail.couldNotSwitch"), message: refusalText(error, t) }),
  });

  return (
    <SectionCard title={t("mail.title")}>
      <Text size="sm">{t("mail.intro")}</Text>
      <Switch
        label={t("mail.switch")}
        description={t("mail.switchDescription")}
        checked={mail.enabled}
        disabled={!owner || save.isPending}
        onChange={(event) => save.mutate(event.currentTarget.checked)}
      />
      {!owner && <OwnerOnly />}
      <Stack gap={0} role="status">
        <Text size="sm" c={colours[mail.status]}>
          {t(`mail.status_${mail.status}`)}
        </Text>
        {mail.status === "unavailable" && mail.reason && (
          <Text size="sm" c="dimmed">
            {mailReasonText(mail.reason, t)}
          </Text>
        )}
        {mail.enabled && (
          <Text size="sm" c="dimmed">
            {t("mail.mailboxes", { n: mail.mailboxes })}
          </Text>
        )}
      </Stack>
      <Alert color="orange" variant="light">
        {t("mail.warning")}
      </Alert>
      <CopyLine label={t("mail.scopes")} value={GMAIL_SCOPES.join(",")} copyLabel={t("mail.scopesCopy")} />
    </SectionCard>
  );
};
