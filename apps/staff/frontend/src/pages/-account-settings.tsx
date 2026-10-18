import { Alert, Anchor, Button, Group, NumberInput, SimpleGrid, Stack, Text } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconPlayerPlay } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { appUrl, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { runAccounts, type StaffAccountsRun } from "../api/accounts";
import type { StaffMeta } from "../api/meta";
import { STAFF_QUERY_KEY } from "../api/request";
import { eraseSettingsOf, type StaffSettings, updateStaffSettings } from "../api/settings";
import { FieldGroup } from "../components/field-group";
import "../i18n";
import { refusalText } from "../lib/absence";
import { useAccountModeText, WORKSPACE_ROLES_PATH } from "../lib/accounts";
import { fieldRefusals, isRevisionConflict } from "../lib/errors";

interface AccountValues {
  createBeforeDays: number | string;
  deleteExternalAfterDays: number | string;
}

const valuesOf = (settings: StaffSettings): AccountValues => ({
  createBeforeDays: settings.createBeforeDays,
  deleteExternalAfterDays: settings.deleteExternalAfterDays ?? "",
});

const wholeWithin = (value: number | string, min: number, max: number) => {
  const days = Number(value);
  return value !== "" && Number.isInteger(days) && days >= min && days <= max;
};

/**
 * The account settings in Settings (delivery C), `staff:manage`'s: the
 * authority mode identity records, whether an Owner has named the roles
 * Staff may give — warned, with the way to identity's roles page, when no
 * Owner has — `createBeforeDays` and the inert `deleteExternalAfterDays`
 * (the Entra module's), saved through `PUT /settings` under the revision
 * read with the general and the erase settings sent back as read, and *Run
 * accounts now*, its counts said in words.
 */
export const AccountSettingsPanel = ({ settings, meta }: { settings: StaffSettings; meta: StaffMeta }) => {
  const { t, formatters } = useI18n("staff");
  const modeText = useAccountModeText();
  const queryClient = useQueryClient();
  const [result, setResult] = useState<StaffAccountsRun | null>(null);

  const form = useForm<AccountValues>({
    initialValues: valuesOf(settings),
    validate: {
      createBeforeDays: (value) => (wholeWithin(value, 0, 90) ? null : t("createBeforeDaysInvalid")),
      deleteExternalAfterDays: (value) =>
        value === "" || wholeWithin(value, 1, 3650) ? null : t("deleteExternalAfterDaysInvalid"),
    },
  });

  // A pristine form follows the server, as the other settings forms do.
  const loaded = JSON.stringify(settings);
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty()) {
      form.setInitialValues(valuesOf(settings));
      form.setValues(valuesOf(settings));
    }
  }

  const save = useMutation({
    mutationFn: (values: AccountValues) =>
      updateStaffSettings(
        {
          timeZone: settings.timeZone,
          ...(settings.employeeNumberPrefix ? { employeeNumberPrefix: settings.employeeNumberPrefix } : {}),
          nextEmployeeNumber: settings.nextEmployeeNumber,
          ...(settings.pensionProvider ? { pensionProvider: settings.pensionProvider } : {}),
          ...(settings.injuryInsurer ? { injuryInsurer: settings.injuryInsurer } : {}),
          createBeforeDays: Number(values.createBeforeDays),
          deleteExternalAfterDays:
            values.deleteExternalAfterDays === "" ? null : Number(values.deleteExternalAfterDays),
          ...eraseSettingsOf(settings),
        },
        settings.revision,
      ),
    onSuccess: async () => {
      form.resetDirty();
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("accountSettingsSaved"), message: "" });
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        form.resetDirty();
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, ["createBeforeDays", "deleteExternalAfterDays"]);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: rest.join(" ") });
    },
  });

  const run = useMutation({
    mutationFn: runAccounts,
    onSuccess: async (answer) => {
      setResult(answer);
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotRunAccounts"), message: refusalText(error, t) }),
  });

  const count = (value: number) => formatters.formatNumber(value);

  return (
    <FieldGroup title={t("accountSettings")} description={t("accountSettingsDescription")}>
      <Text size="sm">{modeText(meta.accountMode)}</Text>
      {meta.provisioningRolesSet === true && <Text size="sm">{t("accountRolesSet")}</Text>}
      {meta.provisioningRolesSet === false && (
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
          <Stack gap={4}>
            <Text size="sm">{t("accountRolesMissing")}</Text>
            <Anchor size="sm" href={appUrl(WORKSPACE_ROLES_PATH)}>
              {t("openRoles")}
            </Anchor>
          </Stack>
        </Alert>
      )}
      <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
        <Stack gap="sm">
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <NumberInput
              label={t("createBeforeDays")}
              description={t("createBeforeDaysDescription")}
              withAsterisk
              allowDecimal={false}
              allowNegative={false}
              thousandSeparator={false}
              {...form.getInputProps("createBeforeDays")}
            />
            <NumberInput
              label={t("deleteExternalAfterDays")}
              description={t("deleteExternalAfterDaysDescription")}
              allowDecimal={false}
              allowNegative={false}
              thousandSeparator={false}
              {...form.getInputProps("deleteExternalAfterDays")}
            />
          </SimpleGrid>
          <Group justify="flex-end">
            <Button type="submit" variant="light" loading={save.isPending}>
              {t("saveAccountSettings")}
            </Button>
          </Group>
        </Stack>
      </form>
      {meta.capabilities.canRunAccounts && (
        <>
          <Text size="sm" c="dimmed">
            {t("runAccountsExplained")}
          </Text>
          <Group>
            <Button
              variant="light"
              leftSection={<IconPlayerPlay size={16} />}
              loading={run.isPending}
              onClick={() => {
                setResult(null);
                run.mutate();
              }}
            >
              {t("runAccountsNow")}
            </Button>
          </Group>
          {result && (
            <Text size="sm" role="status">
              {t("runAccountsResult", {
                attempted: count(result.attempted),
                done: count(result.done),
                failed: count(result.failed),
                remaining: count(result.remaining),
              })}
            </Text>
          )}
        </>
      )}
    </FieldGroup>
  );
};
