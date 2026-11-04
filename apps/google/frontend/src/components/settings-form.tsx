import { Button, Group, NumberInput, Select, SimpleGrid, Stack, Switch, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type GoogleConnection, type GoogleSettingsRequest, updateSettings } from "../api/connection";
import { GOOGLE_QUERY_KEY } from "../api/request";
import { fieldRefusals, isStale } from "../lib/words";

interface SettingsValues {
  addressDomain: string;
  addressPattern: GoogleConnection["addressPattern"];
  orgUnitPath: string;
  passLifetimeDays: number | string;
  passDelivery: GoogleConnection["passDelivery"];
  leaverRemoveLicences: boolean;
  leaverRemoveGroups: GoogleConnection["leaverRemoveGroups"];
  leaverSignOut: boolean;
}

const fieldNames = [
  "addressDomain",
  "addressPattern",
  "orgUnitPath",
  "passLifetimeDays",
  "passDelivery",
  "leaverRemoveLicences",
  "leaverRemoveGroups",
  "leaverSignOut",
] as const;

const valuesOf = (connection: GoogleConnection): SettingsValues => ({
  addressDomain: connection.addressDomain ?? "",
  addressPattern: connection.addressPattern,
  orgUnitPath: connection.orgUnitPath,
  passLifetimeDays: connection.passLifetimeDays,
  passDelivery: connection.passDelivery,
  leaverRemoveLicences: connection.leaverRemoveLicences,
  leaverRemoveGroups: connection.leaverRemoveGroups,
  leaverSignOut: connection.leaverSignOut,
});

const requestOf = (values: SettingsValues): GoogleSettingsRequest => ({
  addressDomain: values.addressDomain.trim().toLowerCase(),
  addressPattern: values.addressPattern,
  orgUnitPath: values.orgUnitPath.trim(),
  passLifetimeDays: Number(values.passLifetimeDays),
  passDelivery: values.passDelivery,
  leaverRemoveLicences: values.leaverRemoveLicences,
  leaverRemoveGroups: values.leaverRemoveGroups,
  leaverSignOut: values.leaverSignOut,
});

/** The Select's value for "no domain set": the primary domain is used then. */
const PRIMARY = "@primary";
const passDays = { min: 1, max: 30 };

/**
 * The account rules (Google Workspace design G17, G19), `google:manage`'s:
 * the address domain among the verified domains (free text before a test
 * has read them; empty for the primary domain), the pattern, the
 * organisational unit, the first password's lifetime and who hands it over,
 * and the leaver's steps — saved under If-Match on the connection's version.
 * A stale version reloads, as the admin form does.
 */
export const SettingsForm = ({ connection }: { connection: GoogleConnection }) => {
  const { t } = useI18n("google");
  const queryClient = useQueryClient();
  const form = useForm<SettingsValues>({
    initialValues: valuesOf(connection),
    validate: {
      orgUnitPath: (value) => (value.trim().startsWith("/") ? null : t("orgUnitPathInvalid")),
      passLifetimeDays: (value) => {
        const days = Number(value);
        return value !== "" && Number.isInteger(days) && days >= passDays.min && days <= passDays.max
          ? null
          : t("passLifetimeInvalid");
      },
    },
  });

  // A pristine form follows the server (another tab's write, a reload after a 412).
  const loaded = JSON.stringify(valuesOf(connection));
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty()) {
      form.setInitialValues(valuesOf(connection));
      form.setValues(valuesOf(connection));
    }
  }

  const save = useMutation({
    mutationFn: (values: SettingsValues) => updateSettings(requestOf(values), connection.version),
    onSuccess: async (answer) => {
      queryClient.setQueryData([GOOGLE_QUERY_KEY, "connection"], answer);
      form.resetDirty();
      notifications.show({ color: "teal", title: t("settingsSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY, "connection"] });
    },
    onError: async (error) => {
      if (isStale(error)) {
        form.resetDirty();
        notifications.show({ color: "yellow", title: t("versionMismatch"), message: t("versionMismatchHint") });
        await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY, "connection"] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, fieldNames, t);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: rest.join(" ") });
    },
  });

  const choices = (keys: readonly string[], prefix: string) =>
    keys.map((key) => ({ value: key, label: t(`${prefix}_${key.replaceAll(".", "_")}`) }));
  const domains = [...new Set([...connection.domains, form.values.addressDomain].filter(Boolean))];
  const primary = connection.primaryDomain
    ? t("addressDomainPrimary", { domain: connection.primaryDomain })
    : t("addressDomainPrimaryUnknown");

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))} aria-label={t("accountRules")}>
      <Stack gap="sm">
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          {connection.domains.length > 0 ? (
            <Select
              label={t("addressDomain")}
              description={t("addressDomainDescription")}
              allowDeselect={false}
              data={[
                { value: PRIMARY, label: primary },
                ...domains.map((domain) => ({ value: domain, label: domain })),
              ]}
              error={form.errors.addressDomain}
              value={form.values.addressDomain || PRIMARY}
              onChange={(value) => form.setFieldValue("addressDomain", !value || value === PRIMARY ? "" : value)}
            />
          ) : (
            <TextInput
              label={t("addressDomain")}
              description={t("addressDomainFreeDescription")}
              autoComplete="off"
              {...form.getInputProps("addressDomain")}
            />
          )}
          <Select
            label={t("addressPattern")}
            description={t("addressPatternDescription")}
            allowDeselect={false}
            data={choices(["given.family", "g.family", "given"], "addressPattern")}
            {...form.getInputProps("addressPattern")}
          />
          <TextInput
            label={t("orgUnitPath")}
            description={t("orgUnitPathDescription")}
            autoComplete="off"
            withAsterisk
            {...form.getInputProps("orgUnitPath")}
          />
        </SimpleGrid>

        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <NumberInput
            label={t("passLifetimeDays")}
            description={t("passLifetimeDaysDescription")}
            min={passDays.min}
            max={passDays.max}
            allowDecimal={false}
            allowNegative={false}
            thousandSeparator={false}
            withAsterisk
            {...form.getInputProps("passLifetimeDays")}
          />
          <Select
            label={t("passDelivery")}
            description={t("passDeliveryDescription")}
            allowDeselect={false}
            data={choices(["hr", "manager", "both"], "passDelivery")}
            {...form.getInputProps("passDelivery")}
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
            label={t("leaverRemoveLicences")}
            description={t("leaverRemoveLicencesDescription")}
            {...form.getInputProps("leaverRemoveLicences", { type: "checkbox" })}
          />
          <Switch label={t("leaverSignOut")} {...form.getInputProps("leaverSignOut", { type: "checkbox" })} />
          <Select
            label={t("leaverRemoveGroups")}
            allowDeselect={false}
            data={choices(["all", "managed", "none"], "leaverRemoveGroups")}
            {...form.getInputProps("leaverRemoveGroups")}
          />
        </SimpleGrid>

        <Group justify="flex-end">
          <Button type="submit" loading={save.isPending}>
            {t("saveSettings")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
