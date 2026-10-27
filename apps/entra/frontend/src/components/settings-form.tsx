import {
  Alert,
  Button,
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
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type EntraCalendarSync,
  type EntraConnection,
  type EntraSettings,
  type EntraSettingsRequest,
  skusQueryOptions,
  updateSettings,
} from "../api/connection";
import { ENTRA_QUERY_KEY } from "../api/request";
import { fieldRefusals, isStale, reasonText, refusalText, type Translate } from "../lib/words";
import { PortalPath } from "./step-card";

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
  calendarSync: boolean;
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
  "calendarSync",
] as const;

const valuesOf = (settings: EntraSettings, calendarSync: EntraCalendarSync): SettingsValues => ({
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
  calendarSync: calendarSync.enabled,
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
  calendarSync: values.calendarSync,
});

const domainPattern = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/;
const passMinutes = { min: 10, max: 43200 };

/**
 * Step 4, the account rules (`entra:manage`'s, not only the Owner's): the
 * UPN domain among the tenant's verified domains (free text before a test
 * has read them), the pattern, the usage location, the leaver steps, the
 * hire date, the Temporary Access Pass and the calendar sync switch (calendar
 * Outlook sync design O2), saved under If-Match on the
 * connection's version; a stale version reloads, as Staff's forms do.
 */
export const SettingsForm = ({ connection }: { connection: EntraConnection }) => {
  const { t, formatters } = useI18n("entra");
  const queryClient = useQueryClient();
  const form = useForm<SettingsValues>({
    initialValues: valuesOf(connection.settings, connection.calendarSync),
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
  const loaded = JSON.stringify([connection.settings, connection.calendarSync.enabled]);
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty()) {
      form.setInitialValues(valuesOf(connection.settings, connection.calendarSync));
      form.setValues(valuesOf(connection.settings, connection.calendarSync));
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

  const domains = connection.verifiedDomains;
  const choices = (keys: readonly string[], prefix: string) =>
    keys.map((key) => ({ value: key, label: t(`${prefix}_${key.replaceAll(".", "_")}`) }));
  const connected = connection.status === "connected";
  const skus = useQuery({ ...skusQueryOptions(), enabled: connected });

  return (
    <Stack gap="sm">
      <form onSubmit={form.onSubmit((values) => save.mutate(values))} aria-label={t("accountRules")}>
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
            {t("calendarSync")}
          </Title>
          <Switch
            label={t("calendarSyncSwitch")}
            description={t("calendarSyncDescription")}
            {...form.getInputProps("calendarSync", { type: "checkbox" })}
          />
          <CalendarSyncStatus calendarSync={connection.calendarSync} t={t} />
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
 * Where calendar sync stands, as the connection answers it: nothing while the
 * switch is off, else ready, waiting for the permission, or waiting for the
 * connection (with the connection's reason when it has one).
 */
const CalendarSyncStatus = ({ calendarSync, t }: { calendarSync: EntraCalendarSync; t: Translate }) => {
  if (calendarSync.status === "off") return null;
  const colour = { ready: "teal", permission_missing: "orange", unavailable: "dimmed" }[calendarSync.status];
  return (
    <Stack gap={0} role="status">
      <Text size="sm" c={colour}>
        {t(`calendarSyncStatus_${calendarSync.status}`)}
      </Text>
      {calendarSync.status === "unavailable" && calendarSync.reason && (
        <Text size="sm" c="dimmed">
          {reasonText(calendarSync.reason, t)}
        </Text>
      )}
    </Stack>
  );
};
