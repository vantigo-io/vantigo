import { Button, Code, Group, Stack, Switch, Text, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type EntraConnection,
  type EntraMailChannels,
  type EntraOutlookConnect,
  type EntraSettingsRequest,
  updateSettings,
} from "../api/connection";
import { ENTRA_QUERY_KEY } from "../api/request";
import { fieldRefusals, isStale, reasonText, type Translate } from "../lib/words";
import { CopyValue } from "./step-card";

interface OutlookValues {
  outlookConnect: boolean;
  mailChannels: boolean;
}

const fieldNames = ["outlookConnect", "mailChannels"] as const;

const valuesOf = (outlookConnect: EntraOutlookConnect, mailChannels: EntraMailChannels): OutlookValues => ({
  outlookConnect: outlookConnect.enabled,
  mailChannels: mailChannels.enabled,
});

/** Only the two switches: the settings request keeps every field it leaves out. */
const requestOf = (values: OutlookValues): EntraSettingsRequest => ({
  outlookConnect: values.outlookConnect,
  mailChannels: values.mailChannels,
});

/**
 * The **Outlook and mail** tab's one form: the Outlook connection switch
 * (Outlook connection design M2) with its redirect URI, status and change
 * notifications, and the Mail channels switch (M10) with its status and the
 * Exchange Online commands. It was the end of step 4's form; it saves under
 * If-Match on the connection's version as that form did, but sends only
 * these two fields — the account rules are left as they are.
 */
export const OutlookSettingsForm = ({ connection }: { connection: EntraConnection }) => {
  const { t, formatters } = useI18n("entra");
  const formatTime = (value: string) =>
    formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" });
  const queryClient = useQueryClient();
  const form = useForm<OutlookValues>({
    initialValues: valuesOf(connection.outlookConnect, connection.mailChannels),
  });

  // A pristine form follows the server, as Staff's settings forms do.
  const loaded = JSON.stringify([connection.outlookConnect.enabled, connection.mailChannels.enabled]);
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty()) {
      form.setInitialValues(valuesOf(connection.outlookConnect, connection.mailChannels));
      form.setValues(valuesOf(connection.outlookConnect, connection.mailChannels));
    }
  }

  const save = useMutation({
    mutationFn: (values: OutlookValues) => updateSettings(requestOf(values), connection.version),
    onSuccess: async (answer) => {
      queryClient.setQueryData([ENTRA_QUERY_KEY, "connection"], answer);
      form.resetDirty();
      notifications.show({ color: "teal", title: t("outlookSettingsSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [ENTRA_QUERY_KEY] });
    },
    onError: async (error) => {
      if (isStale(error)) {
        form.resetDirty();
        notifications.show({ color: "yellow", title: t("versionMismatch"), message: t("versionMismatchHint") });
        await queryClient.invalidateQueries({ queryKey: [ENTRA_QUERY_KEY] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, fieldNames, t);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSaveOutlookSettings"), message: rest.join(" ") });
    },
  });

  // Turning the switch off closes everyone's connection (design M7), so the page says so before it saves.
  const submit = (values: OutlookValues) => {
    const { enabled, connected, reconnect } = connection.outlookConnect;
    if (!enabled || values.outlookConnect || connected + reconnect === 0) {
      save.mutate(values);
      return;
    }
    modals.openConfirmModal({
      title: t("outlookConnectOffTitle"),
      children: <Text size="sm">{t("outlookConnectOffBody")}</Text>,
      labels: { confirm: t("outlookConnectOffConfirm"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => save.mutate(values),
    });
  };

  return (
    <form onSubmit={form.onSubmit(submit)} aria-label={t("outlookAndMailSettings")}>
      <Stack gap="sm">
        <Title order={4} fz="h6" mt="xs">
          {t("outlookConnect")}
        </Title>
        <Switch
          label={t("outlookConnectSwitch")}
          description={t("outlookConnectDescription")}
          {...form.getInputProps("outlookConnect", { type: "checkbox" })}
        />
        <Stack gap={0}>
          <Text size="sm" fw={500}>
            {t("outlookRedirectUri")}
          </Text>
          <Group gap={4} wrap="nowrap">
            <Code style={{ overflowWrap: "anywhere" }}>{connection.outlookConnect.redirectUri}</Code>
            <CopyValue value={connection.outlookConnect.redirectUri} />
          </Group>
        </Stack>
        <OutlookConnectStatus outlookConnect={connection.outlookConnect} t={t} />
        <ChangeNotifications outlookConnect={connection.outlookConnect} t={t} formatTime={formatTime} />

        <Title order={4} fz="h6" mt="xs">
          {t("mailChannels")}
        </Title>
        <Switch
          label={t("mailChannelsSwitch")}
          description={t("mailChannelsDescription")}
          {...form.getInputProps("mailChannels", { type: "checkbox" })}
        />
        <MailChannelsStatus mailChannels={connection.mailChannels} t={t} />
        {form.values.mailChannels && (
          <MailChannelsCommands
            clientId={connection.clientId}
            servicePrincipalId={connection.mailChannels.servicePrincipalId}
            t={t}
          />
        )}
        <Group justify="flex-end">
          <Button type="submit" loading={save.isPending}>
            {t("saveOutlookSettings")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/**
 * Where the Outlook connection stands, as the connection answers it: nothing
 * while the switch is off, else ready, untested, consent missing, or
 * waiting for the connection (with the connection's reason when it has one), and how many
 * people are connected and waiting to reconnect.
 */
const OutlookConnectStatus = ({ outlookConnect, t }: { outlookConnect: EntraOutlookConnect; t: Translate }) => {
  if (outlookConnect.status === "off") return null;
  const colour = {
    ready: "teal",
    untested: "blue",
    consent_missing: "orange",
    unavailable: "dimmed",
  }[outlookConnect.status];
  return (
    <Stack gap={0} role="status">
      <Text size="sm" c={colour}>
        {t(`outlookConnectStatus_${outlookConnect.status}`)}
      </Text>
      {outlookConnect.status === "unavailable" && outlookConnect.reason && (
        <Text size="sm" c="dimmed">
          {reasonText(outlookConnect.reason, t)}
        </Text>
      )}
      <Text size="sm" c="dimmed">
        {t("outlookConnectCounts", { connected: outlookConnect.connected, reconnect: outlookConnect.reconnect })}
      </Text>
    </Stack>
  );
};

/**
 * The change notifications' state, under the sync's own: nothing while the
 * switch is off (calendar Outlook sync design O11.7). Active names the
 * mailboxes watched and the last notification; the others say why changes
 * arrive with the next sync instead.
 */
const ChangeNotifications = ({
  outlookConnect,
  t,
  formatTime,
}: {
  outlookConnect: EntraOutlookConnect;
  t: Translate;
  formatTime: (value: string) => string;
}) => {
  if (outlookConnect.status === "off") return null;
  const { status, subscriptions, lastNotificationAt, unreachableAt } = outlookConnect.notifications;
  const colour = { active: "teal", off: "dimmed", not_configured: "orange", unreachable: "orange" }[status];
  const text =
    status === "active"
      ? t("changeNotifications_active", {
          count: subscriptions,
          last: lastNotificationAt ? formatTime(lastNotificationAt) : t("changeNotificationsNoneYet"),
        })
      : status === "unreachable"
        ? t("changeNotifications_unreachable", { since: unreachableAt ? formatTime(unreachableAt) : "" })
        : t(`changeNotifications_${status}`);
  return (
    <Stack gap={0} aria-label={t("changeNotifications")} role="group">
      <Text size="sm" fw={500}>
        {t("changeNotifications")}
      </Text>
      <Text size="sm" c={colour}>
        {text}
      </Text>
    </Stack>
  );
};

/**
 * Where the Mail channels stand, as the connection answers it (design M10):
 * nothing while the switch is off, else ready (no mail permission reaching
 * the whole tenant), tenant_wide (a warning, never a block), or waiting for
 * the connection with its reason.
 */
const MailChannelsStatus = ({ mailChannels, t }: { mailChannels: EntraMailChannels; t: Translate }) => {
  if (mailChannels.status === "off") return null;
  const colour = { ready: "teal", tenant_wide: "orange", unavailable: "dimmed" }[mailChannels.status];
  return (
    <Stack gap={0} role="status" aria-label={t("mailChannels")}>
      <Text size="sm" c={colour}>
        {t(`mailChannelsStatus_${mailChannels.status}`)}
      </Text>
      {mailChannels.status === "unavailable" && mailChannels.reason && (
        <Text size="sm" c="dimmed">
          {reasonText(mailChannels.reason, t)}
        </Text>
      )}
    </Stack>
  );
};

/**
 * The RBAC for Applications commands the administration guide's Mail channels
 * procedure runs, with the client id and the service principal's object id
 * filled in (a placeholder until a test has read it), ready to copy.
 */
const MailChannelsCommands = ({
  clientId,
  servicePrincipalId,
  t,
}: {
  clientId?: string;
  servicePrincipalId?: string;
  t: Translate;
}) => {
  const app = clientId ?? t("mailChannelsClientIdPlaceholder");
  const objectId = servicePrincipalId ?? t("mailChannelsObjectIdPlaceholder");
  const scope = "Vantigo mail channels";
  const commands = [
    `New-ServicePrincipal -AppId ${app} -ObjectId ${objectId} -DisplayName "Vantigo"`,
    `Set-Mailbox post@firma.no -CustomAttribute10 "Vantigo"`,
    `New-ManagementScope -Name "${scope}" -RecipientRestrictionFilter "CustomAttribute10 -eq 'Vantigo'"`,
    `New-ManagementRoleAssignment -App ${app} -Role "Application Mail Full Access" -CustomResourceScope "${scope}"`,
    `New-ManagementRoleAssignment -App ${app} -Role "Application MailboxSettings.Read" -CustomResourceScope "${scope}"`,
    `Test-ServicePrincipalAuthorization -Identity ${app} -Resource post@firma.no`,
  ].join("\n");
  return (
    <Stack gap={4} role="group" aria-label={t("mailChannelsCommands")}>
      <Text size="sm" fw={500}>
        {t("mailChannelsCommands")}
      </Text>
      <Text size="sm" c="dimmed">
        {t("mailChannelsCommandsDescription")}
      </Text>
      {!servicePrincipalId && (
        <Text size="sm" c="orange">
          {t("mailChannelsTestFirst")}
        </Text>
      )}
      <Group gap={4} wrap="nowrap" align="flex-start">
        <Code block style={{ flex: 1, minWidth: 0 }}>
          {commands}
        </Code>
        <CopyValue value={commands} label={t("mailChannelsCommands")} />
      </Group>
    </Stack>
  );
};
