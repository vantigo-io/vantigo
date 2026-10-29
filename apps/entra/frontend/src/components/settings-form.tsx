import {
  Alert,
  Button,
  Code,
  Group,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type EntraConnection,
  type EntraMailChannels,
  type EntraOutlookConnect,
  type EntraSettings,
  type EntraSettingsRequest,
  skusQueryOptions,
  updateSettings,
} from "../api/connection";
import { ENTRA_QUERY_KEY } from "../api/request";
import { fieldRefusals, isStale, reasonText, refusalText, type Translate } from "../lib/words";
import { CopyValue, PortalPath } from "./step-card";

interface SettingsValues {
  upnDomain: string;
  upnPattern: EntraSettings["upnPattern"];
  defaultUsageLocation: string;
  leaverStripLicences: boolean;
  leaverRemoveGroups: EntraSettings["leaverRemoveGroups"];
  leaverRevokeSessions: boolean;
  leaverWriteLeaveDate: boolean;
  writeHireDate: boolean;
  passLifetimeMinutes: number | string;
  passUsableOnce: boolean;
  passDelivery: EntraSettings["passDelivery"];
  outlookConnect: boolean;
  mailChannels: boolean;
}

const fieldNames = [
  "upnDomain",
  "upnPattern",
  "defaultUsageLocation",
  "leaverStripLicences",
  "leaverRemoveGroups",
  "leaverRevokeSessions",
  "leaverWriteLeaveDate",
  "writeHireDate",
  "passLifetimeMinutes",
  "passUsableOnce",
  "passDelivery",
  "outlookConnect",
  "mailChannels",
] as const;

const valuesOf = (
  settings: EntraSettings,
  outlookConnect: EntraOutlookConnect,
  mailChannels: EntraMailChannels,
): SettingsValues => ({
  upnDomain: settings.upnDomain ?? "",
  upnPattern: settings.upnPattern,
  defaultUsageLocation: settings.defaultUsageLocation,
  leaverStripLicences: settings.leaverStripLicences,
  leaverRemoveGroups: settings.leaverRemoveGroups,
  leaverRevokeSessions: settings.leaverRevokeSessions,
  leaverWriteLeaveDate: settings.leaverWriteLeaveDate,
  writeHireDate: settings.writeHireDate,
  passLifetimeMinutes: settings.passLifetimeMinutes,
  passUsableOnce: settings.passUsableOnce,
  passDelivery: settings.passDelivery,
  outlookConnect: outlookConnect.enabled,
  mailChannels: mailChannels.enabled,
});

const requestOf = (values: SettingsValues): EntraSettingsRequest => ({
  upnDomain: values.upnDomain.trim().toLowerCase(),
  upnPattern: values.upnPattern,
  defaultUsageLocation: values.defaultUsageLocation.trim().toUpperCase(),
  leaverStripLicences: values.leaverStripLicences,
  leaverRemoveGroups: values.leaverRemoveGroups,
  leaverRevokeSessions: values.leaverRevokeSessions,
  leaverWriteLeaveDate: values.leaverWriteLeaveDate,
  writeHireDate: values.writeHireDate,
  passLifetimeMinutes: Number(values.passLifetimeMinutes),
  passUsableOnce: values.passUsableOnce,
  passDelivery: values.passDelivery,
  outlookConnect: values.outlookConnect,
  mailChannels: values.mailChannels,
});

const domainPattern = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/;
const passMinutes = { min: 10, max: 43200 };

/**
 * Step 4, the account rules (`entra:manage`'s, not only the Owner's): the
 * UPN domain among the tenant's verified domains (free text before a test
 * has read them), the pattern, the usage location, the leaver steps, the
 * hire date, the Temporary Access Pass, the Outlook connection switch (Outlook
 * connection design M2) and the Mail channels switch (M10), saved under If-Match on the
 * connection's version; a stale version reloads, as Staff's forms do.
 */
export const SettingsForm = ({ connection }: { connection: EntraConnection }) => {
  const { t, formatters } = useI18n("entra");
  const formatTime = (value: string) =>
    formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" });
  const queryClient = useQueryClient();
  const form = useForm<SettingsValues>({
    initialValues: valuesOf(connection.settings, connection.outlookConnect, connection.mailChannels),
    validate: {
      upnDomain: (value) => {
        const domain = value.trim().toLowerCase();
        if (!domain) return t("upnDomainRequired");
        return domainPattern.test(domain) ? null : t("upnDomainInvalid");
      },
      defaultUsageLocation: (value) => (/^[A-Za-z]{2}$/.test(value.trim()) ? null : t("usageLocationInvalid")),
      passLifetimeMinutes: (value) => {
        const minutes = Number(value);
        return value !== "" && Number.isInteger(minutes) && minutes >= passMinutes.min && minutes <= passMinutes.max
          ? null
          : t("passLifetimeInvalid", passMinutes);
      },
    },
  });

  // A pristine form follows the server, as Staff's settings forms do.
  const loaded = JSON.stringify([
    connection.settings,
    connection.outlookConnect.enabled,
    connection.mailChannels.enabled,
  ]);
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty()) {
      form.setInitialValues(valuesOf(connection.settings, connection.outlookConnect, connection.mailChannels));
      form.setValues(valuesOf(connection.settings, connection.outlookConnect, connection.mailChannels));
    }
  }

  const save = useMutation({
    mutationFn: (values: SettingsValues) => updateSettings(requestOf(values), connection.version),
    onSuccess: async (answer) => {
      queryClient.setQueryData([ENTRA_QUERY_KEY, "connection"], answer);
      form.resetDirty();
      notifications.show({ color: "teal", title: t("settingsSaved"), message: "" });
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
        notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: rest.join(" ") });
    },
  });

  // Turning the switch off closes everyone's connection (design M7), so the page says so before it saves.
  const submit = (values: SettingsValues) => {
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

  const domains = connection.verifiedDomains;
  const choices = (keys: readonly string[], prefix: string) =>
    keys.map((key) => ({ value: key, label: t(`${prefix}_${key.replaceAll(".", "_")}`) }));
  const connected = connection.status === "connected";
  const skus = useQuery({ ...skusQueryOptions(), enabled: connected });

  return (
    <Stack gap="sm">
      <form onSubmit={form.onSubmit(submit)} aria-label={t("accountRules")}>
        <Stack gap="sm">
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            {domains.length > 0 ? (
              <Select
                label={t("upnDomain")}
                description={t("upnDomainDescription")}
                withAsterisk
                allowDeselect={false}
                data={[...new Set([...domains, form.values.upnDomain].filter(Boolean))]}
                {...form.getInputProps("upnDomain")}
                value={form.values.upnDomain || null}
                onChange={(value) => form.setFieldValue("upnDomain", value ?? "")}
              />
            ) : (
              <TextInput
                label={t("upnDomain")}
                description={t("upnDomainFreeDescription")}
                withAsterisk
                {...form.getInputProps("upnDomain")}
              />
            )}
            <Select
              label={t("upnPattern")}
              description={t("upnPatternDescription")}
              allowDeselect={false}
              data={choices(["given.family", "g.family", "given"], "upnPattern")}
              {...form.getInputProps("upnPattern")}
            />
            <TextInput
              label={t("defaultUsageLocation")}
              description={t("defaultUsageLocationDescription")}
              maxLength={2}
              withAsterisk
              {...form.getInputProps("defaultUsageLocation")}
            />
          </SimpleGrid>

          <Title order={4} fz="h6" mt="xs">
            {t("leaverSteps")}
          </Title>
          <Text size="sm" c="dimmed">
            {t("leaverStepsDescription")}
          </Text>
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <Switch
              label={t("leaverRevokeSessions")}
              {...form.getInputProps("leaverRevokeSessions", { type: "checkbox" })}
            />
            <Switch
              label={t("leaverWriteLeaveDate")}
              {...form.getInputProps("leaverWriteLeaveDate", { type: "checkbox" })}
            />
            <Switch
              label={t("leaverStripLicences")}
              {...form.getInputProps("leaverStripLicences", { type: "checkbox" })}
            />
            <Select
              label={t("leaverRemoveGroups")}
              allowDeselect={false}
              data={choices(["all", "managed", "none"], "leaverRemoveGroups")}
              {...form.getInputProps("leaverRemoveGroups")}
            />
          </SimpleGrid>

          <Title order={4} fz="h6" mt="xs">
            {t("joiner")}
          </Title>
          <Switch
            label={t("writeHireDate")}
            description={t("writeHireDateDescription")}
            {...form.getInputProps("writeHireDate", { type: "checkbox" })}
          />
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <NumberInput
              label={t("passLifetimeMinutes")}
              description={t("passLifetimeMinutesDescription")}
              allowDecimal={false}
              allowNegative={false}
              thousandSeparator={false}
              withAsterisk
              {...form.getInputProps("passLifetimeMinutes")}
            />
            <Select
              label={t("passDelivery")}
              description={t("passDeliveryDescription")}
              allowDeselect={false}
              data={choices(["hr", "manager", "both"], "passDelivery")}
              {...form.getInputProps("passDelivery")}
            />
          </SimpleGrid>
          <Switch label={t("passUsableOnce")} {...form.getInputProps("passUsableOnce", { type: "checkbox" })} />
          <Alert color="blue" variant="light">
            <Stack gap={4}>
              <Text size="sm">{t("tapPolicy")}</Text>
              <PortalPath path={t("tapPolicyPath")} />
            </Stack>
          </Alert>

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
              {t("saveSettings")}
            </Button>
          </Group>
        </Stack>
      </form>

      {connected && (
        <Stack gap={4}>
          <Title order={4} fz="h6">
            {t("licences")}
          </Title>
          <Text size="sm" c="dimmed">
            {t("licencesDescription")}
          </Text>
          {skus.isError && (
            <Text size="sm" c="red">
              {refusalText(skus.error, t)}
            </Text>
          )}
          {skus.data && skus.data.length === 0 && <Text size="sm">{t("noLicences")}</Text>}
          {skus.data?.map((sku) => (
            <Text size="sm" key={sku.skuId}>
              {t("licenceSeats", {
                part: sku.skuPartNumber,
                consumed: formatters.formatNumber(sku.consumed),
                enabled: formatters.formatNumber(sku.enabled),
              })}
            </Text>
          ))}
        </Stack>
      )}
    </Stack>
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
