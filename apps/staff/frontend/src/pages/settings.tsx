import {
  Alert,
  Button,
  Card,
  Group,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { staffMetaQueryOptions } from "../api/meta";
import { STAFF_QUERY_KEY } from "../api/request";
import { type StaffSettings, staffSettingsQueryOptions, updateStaffSettings } from "../api/settings";
import "../i18n";
import {
  EMPLOYEE_NUMBER_MAX_LENGTH,
  EMPLOYEE_NUMBER_PREFIX_MAX_LENGTH,
  NUMBER_CHARACTERS,
  nextGeneratedNumber,
} from "../lib/employee-number";
import { fieldRefusals, isRevisionConflict } from "../lib/errors";
import { AbsenceTypesTable } from "./-absence-types";
import { DocumentCategoriesTable } from "./-document-categories";
import { HolidaySchemesTable } from "./-holiday-schemes";
import { RegisterDataCard } from "./-import-modal";
import { PayrollExportCard } from "./-payroll-export";
import { RetentionPanel } from "./-retention-panel";
import { SelfCertPolicyForm } from "./-self-cert-policy";
import { BusinessUnitsTable, DepartmentsTable, LegalEntitiesTable, LocationsTable } from "./-structure-tables";
import { ThresholdsPanel } from "./-thresholds-panel";

/**
 * Staff settings (design §7): the A1 settings — the business zone, how an
 * employee number is generated, the pension provider and the insurer — the
 * company's structure, the document categories, and A2's data operations:
 * the register's import and export, the payroll month, retention and the
 * thresholds — and delivery B's absence types, holiday schemes, the
 * egenmelding policy and automatic carry-over. The page is `staff:manage`'s; the navigation offers it to
 * that permission alone, and the data cards are drawn only for a reader
 * `/meta` says holds it (`canEdit`), so a colleague who types the address
 * sees none of them.
 */
export const SettingsPage = () => {
  const { t } = useI18n("staff");
  const settings = useQuery(staffSettingsQueryOptions());
  const meta = useQuery(staffMetaQueryOptions());
  const capabilities = meta.data?.capabilities;

  return (
    <Stack gap="lg">
      <PageHeader title={t("settingsTitle")} description={t("settingsDescription")} />
      {settings.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadSettings")}>
          {settings.error.message}
        </Alert>
      )}
      {settings.isPending && <ContentSkeleton rows={4} rowHeight={52} />}
      {settings.data && <GeneralForm settings={settings.data} />}
      <LegalEntitiesTable />
      <BusinessUnitsTable />
      <DepartmentsTable />
      <LocationsTable />
      <DocumentCategoriesTable />
      {capabilities?.canEdit && meta.data && (
        <>
          <AbsenceTypesTable />
          <HolidaySchemesTable />
          {settings.data && <SelfCertPolicyForm settings={settings.data} />}
          <RegisterDataCard sealedAllowed={capabilities.canRevealIdentifiers} />
          <PayrollExportCard today={meta.data.today} />
          {settings.data && <RetentionPanel settings={settings.data} />}
          {settings.data && <ThresholdsPanel acknowledged={settings.data.thresholdsAcknowledged} />}
        </>
      )}
    </Stack>
  );
};

interface GeneralFormValues {
  timeZone: string;
  employeeNumberPrefix: string;
  nextEmployeeNumber: number | string;
  pensionProvider: string;
  injuryInsurer: string;
}

const formValuesOf = (settings: StaffSettings): GeneralFormValues => ({
  timeZone: settings.timeZone,
  employeeNumberPrefix: settings.employeeNumberPrefix,
  nextEmployeeNumber: settings.nextEmployeeNumber,
  pensionProvider: settings.pensionProvider ?? "",
  injuryInsurer: settings.injuryInsurer ?? "",
});

/**
 * Every IANA name this browser knows, so the zone is picked rather than
 * typed — and the zone the installation already runs on even when the
 * browser has never heard of it, or a save would silently clear it.
 */
const timeZoneOptions = (current: string): string[] => {
  const known = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
  return known.includes(current) ? [...known] : [current, ...known];
};

const shownFields = ["timeZone", "employeeNumberPrefix", "nextEmployeeNumber", "pensionProvider", "injuryInsurer"];

const GeneralForm = ({ settings }: { settings: StaffSettings }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();

  /** The number the next person would get, and whether it fits an employee number at all. */
  const tooLong = (values: GeneralFormValues): string | null => {
    const next = Number(values.nextEmployeeNumber);
    if (!Number.isInteger(next) || next < 1) return null;
    const number = nextGeneratedNumber(values.employeeNumberPrefix.trim(), next);
    return number.length > EMPLOYEE_NUMBER_MAX_LENGTH
      ? t("generatedNumberTooLong", { number, length: number.length, max: EMPLOYEE_NUMBER_MAX_LENGTH })
      : null;
  };

  const form = useForm<GeneralFormValues>({
    initialValues: formValuesOf(settings),
    validate: {
      timeZone: (value) => (value.trim() ? null : t("timeZoneRequired")),
      employeeNumberPrefix: (value, values) =>
        value.trim().length > EMPLOYEE_NUMBER_PREFIX_MAX_LENGTH || !NUMBER_CHARACTERS.test(value.trim())
          ? t("prefixInvalid", { max: EMPLOYEE_NUMBER_PREFIX_MAX_LENGTH })
          : tooLong(values),
      nextEmployeeNumber: (value, values) => {
        const next = Number(value);
        return value === "" || !Number.isInteger(next) || next < 1 ? t("nextNumberInvalid") : tooLong(values);
      },
      pensionProvider: (value) => (value.trim().length > 200 ? t("textTooLong", { max: 200 }) : null),
      injuryInsurer: (value) => (value.trim().length > 200 ? t("textTooLong", { max: 200 }) : null),
    },
  });

  // A pristine form follows the server, so a revision moved by a save made
  // elsewhere (a generated number moves `nextEmployeeNumber`) is the one the
  // next save sends; one being typed into is left alone. State adjusted
  // during render from the previous render's value, the way React documents.
  const loaded = JSON.stringify(settings);
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty()) {
      const next = formValuesOf(settings);
      form.setInitialValues(next);
      form.setValues(next);
    }
  }

  const save = useMutation({
    mutationFn: (values: GeneralFormValues) => {
      const optional = (value: string) => (value.trim() ? value.trim() : undefined);
      return updateStaffSettings(
        {
          timeZone: values.timeZone,
          ...(optional(values.employeeNumberPrefix)
            ? { employeeNumberPrefix: values.employeeNumberPrefix.trim() }
            : {}),
          nextEmployeeNumber: Number(values.nextEmployeeNumber),
          ...(optional(values.pensionProvider) ? { pensionProvider: values.pensionProvider.trim() } : {}),
          ...(optional(values.injuryInsurer) ? { injuryInsurer: values.injuryInsurer.trim() } : {}),
        },
        settings.revision,
      );
    },
    onSuccess: async () => {
      form.resetDirty();
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("settingsSaved"), message: "" });
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        // Somebody else saved first: the form takes the fresh row, which a
        // form with unsaved edits would otherwise refuse to follow.
        form.resetDirty();
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, shownFields);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: rest.join(" ") });
    },
  });

  const next = Number(form.values.nextEmployeeNumber);
  const preview =
    Number.isInteger(next) && next >= 1
      ? nextGeneratedNumber(form.values.employeeNumberPrefix.trim(), next)
      : undefined;

  return (
    <Card withBorder padding="lg" radius="md">
      <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
        <Stack>
          <Title order={5}>{t("generalSettings")}</Title>
          <Select
            label={t("businessTimeZone")}
            description={t("businessTimeZoneDescription")}
            withAsterisk
            searchable
            allowDeselect={false}
            data={timeZoneOptions(settings.timeZone)}
            {...form.getInputProps("timeZone")}
          />
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <TextInput
              label={t("employeeNumberPrefix")}
              description={t("employeeNumberPrefixDescription", { max: EMPLOYEE_NUMBER_PREFIX_MAX_LENGTH })}
              {...form.getInputProps("employeeNumberPrefix")}
            />
            <NumberInput
              label={t("nextEmployeeNumber")}
              description={t("nextEmployeeNumberDescription")}
              withAsterisk
              min={1}
              allowDecimal={false}
              allowNegative={false}
              thousandSeparator={false}
              {...form.getInputProps("nextEmployeeNumber")}
            />
          </SimpleGrid>
          {/* Said as a sentence, not left to the 400: the number is generated
              unpadded and an employee number holds at most 16 characters
              (Entra's employeeId), so the length is the thing to watch. */}
          {preview && (
            <Text size="sm" c={preview.length > EMPLOYEE_NUMBER_MAX_LENGTH ? "red" : "dimmed"}>
              {t("nextNumberPreview", { number: preview, length: preview.length, max: EMPLOYEE_NUMBER_MAX_LENGTH })}
            </Text>
          )}
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <TextInput
              label={t("pensionProvider")}
              description={t("pensionProviderDescription")}
              {...form.getInputProps("pensionProvider")}
            />
            <TextInput label={t("injuryInsurer")} {...form.getInputProps("injuryInsurer")} />
          </SimpleGrid>
          <Group justify="flex-end">
            <Button type="submit" loading={save.isPending}>
              {t("saveSettings")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Card>
  );
};
