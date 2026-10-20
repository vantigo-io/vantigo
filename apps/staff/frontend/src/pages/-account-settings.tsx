import { Alert, Anchor, Button, Group, NumberInput, Select, SimpleGrid, Stack, Switch, Text } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconPlayerPlay } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { appUrl, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { runAccounts, type StaffAccountKind, type StaffAccountsRun } from "../api/accounts";
import type { StaffMeta } from "../api/meta";
import { ApiValidationError, STAFF_QUERY_KEY } from "../api/request";
import {
  accountSettingsOf,
  eraseSettingsOf,
  type StaffSettings,
  type StaffSettingsInput,
  updateStaffSettings,
} from "../api/settings";
import { FieldGroup } from "../components/field-group";
import { staffCatalog } from "../i18n";
import { refusalText } from "../lib/absence";
import { accountKindsOffered, providerStatusText, WORKSPACE_ROLES_PATH } from "../lib/accounts";
import { fieldRefusals, isRevisionConflict } from "../lib/errors";

interface AccountValues {
  createBeforeDays: number | string;
  deleteExternalAfterDays: number | string;
  defaultAccountKind: StaffAccountKind;
}

const valuesOf = (settings: StaffSettings): AccountValues => ({
  createBeforeDays: settings.createBeforeDays,
  deleteExternalAfterDays: settings.deleteExternalAfterDays ?? "",
  defaultAccountKind: settings.defaultAccountKind,
});

/** The general settings as read, which a replace of the settings sends back whole. */
const generalSettingsOf = (settings: StaffSettings): StaffSettingsInput => ({
  ...(settings.employeeNumberPrefix ? { employeeNumberPrefix: settings.employeeNumberPrefix } : {}),
  nextEmployeeNumber: settings.nextEmployeeNumber,
  ...(settings.pensionProvider ? { pensionProvider: settings.pensionProvider } : {}),
  ...(settings.injuryInsurer ? { injuryInsurer: settings.injuryInsurer } : {}),
});

const wholeWithin = (value: number | string, min: number, max: number) => {
  const days = Number(value);
  return value !== "" && Number.isInteger(days) && days >= min && days <= max;
};

/**
 * The account settings in Settings (delivery C; Entra B, D5, D8),
 * `staff:manage`'s: each identity provider's status, whether an Owner has
 * named the roles Staff may give — warned, with the way to identity's roles
 * page, when no Owner has — the **Default account kind** (`entra` offered
 * only while `/meta` lists its provider), `createBeforeDays` and
 * `deleteExternalAfterDays`, saved through `PUT /settings` under the
 * revision read with the general and the erase settings sent back as read,
 * and *Run accounts now*, its counts said in words; then the **Directory
 * profile**'s switches, saved the same way.
 */
export const AccountSettingsPanel = ({ settings, meta }: { settings: StaffSettings; meta: StaffMeta }) => (
  <>
    <AccountsCard settings={settings} meta={meta} />
    <DirectoryProfileCard settings={settings} />
  </>
);

const AccountsCard = ({ settings, meta }: { settings: StaffSettings; meta: StaffMeta }) => {
  const { t, formatters } = useI18n("staff");
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
          ...generalSettingsOf(settings),
          createBeforeDays: Number(values.createBeforeDays),
          deleteExternalAfterDays:
            values.deleteExternalAfterDays === "" ? null : Number(values.deleteExternalAfterDays),
          ...eraseSettingsOf(settings),
          // Sent only when changed: left out, the server keeps it, so a
          // provider gone from /meta never refuses a change of the days.
          ...(values.defaultAccountKind === settings.defaultAccountKind
            ? {}
            : { defaultAccountKind: values.defaultAccountKind }),
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
      const { fields, rest } = fieldRefusals(error, [
        "createBeforeDays",
        "deleteExternalAfterDays",
        "defaultAccountKind",
      ]);
      // The kind's field error is provider_missing, whose code the client does not keep.
      if (error instanceof ApiValidationError && fields.defaultAccountKind)
        fields.defaultAccountKind = t("refusal.provider_missing");
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
      {meta.providers && meta.providers.length === 0 && <Text size="sm">{t("providersNone")}</Text>}
      {meta.providers?.map((provider) => (
        <Text size="sm" key={provider.key} c={provider.status === "connected" ? undefined : "orange"}>
          {providerStatusText(t, provider)}
        </Text>
      ))}
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
          <Select
            label={t("defaultAccountKind")}
            description={t("defaultAccountKindDescription")}
            allowDeselect={false}
            data={accountKindsOffered(meta.providers, settings.defaultAccountKind).map((kind) => ({
              value: kind,
              label: `accountKindChoice_${kind}` in staffCatalog.en ? t(`accountKindChoice_${kind}`) : kind,
            }))}
            {...form.getInputProps("defaultAccountKind")}
          />
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

/** The switchable fields of the directory profile (Entra design D8), in the order the Account tab lists them. */
const directoryFieldOrder = [
  "employeeHireDate",
  "employeeLeaveDateTime",
  "employeeType",
  "jobTitle",
  "department",
  "companyName",
  "employeeOrgData",
  "officeLocation",
  "workAddress",
  "phones",
  "preferredLanguage",
  "manager",
];

/** The settings' switches in the order above, a key a later server adds after them. */
const orderedKeys = (fields: Record<string, boolean>) => [
  ...directoryFieldOrder.filter((key) => key in fields),
  ...Object.keys(fields).filter((key) => !directoryFieldOrder.includes(key)),
];

/**
 * The **Directory profile** (Entra design D8), `staff:manage`'s: one switch
 * per switchable field a provider writes on a person's external account,
 * saved through `PUT /settings` under the revision read with every other
 * setting sent back as read. A field switched off is cleared in the
 * directory on the next update; the fields that are always written are
 * listed on the person's Account tab, greyed.
 */
const DirectoryProfileCard = ({ settings }: { settings: StaffSettings }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const form = useForm<{ fields: Record<string, boolean> }>({ initialValues: { fields: settings.directoryFields } });

  const loaded = JSON.stringify(settings);
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty()) {
      form.setInitialValues({ fields: settings.directoryFields });
      form.setValues({ fields: settings.directoryFields });
    }
  }

  const save = useMutation({
    mutationFn: (fields: Record<string, boolean>) =>
      updateStaffSettings(
        {
          ...generalSettingsOf(settings),
          ...accountSettingsOf(settings),
          ...eraseSettingsOf(settings),
          directoryFields: fields,
        },
        settings.revision,
      ),
    onSuccess: async () => {
      form.resetDirty();
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("directoryProfileSaved"), message: "" });
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        form.resetDirty();
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: refusalText(error, t) });
    },
  });

  const label = (key: string) => (`directoryField_${key}` in staffCatalog.en ? t(`directoryField_${key}`) : key);
  return (
    <FieldGroup title={t("directoryProfile")} description={t("directoryProfileDescription")}>
      <form onSubmit={form.onSubmit((values) => save.mutate(values.fields))}>
        <Stack gap="sm">
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
            {orderedKeys(form.values.fields).map((key) => (
              <Switch key={key} label={label(key)} {...form.getInputProps(`fields.${key}`, { type: "checkbox" })} />
            ))}
          </SimpleGrid>
          <Group justify="flex-end">
            <Button type="submit" variant="light" loading={save.isPending}>
              {t("saveDirectoryProfile")}
            </Button>
          </Group>
        </Stack>
      </form>
    </FieldGroup>
  );
};
