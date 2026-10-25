import { Button, Group, NumberInput, Select, SimpleGrid, Stack, Switch, Text } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { StaffSelfCertPolicy } from "../api/absence";
import { STAFF_QUERY_KEY } from "../api/request";
import {
  accountSettingsOf,
  eraseSettingsOf,
  generalSettingsOf,
  type StaffSettings,
  updateStaffSettings,
} from "../api/settings";
import { FieldGroup } from "../components/field-group";
import "../i18n";
import { fieldRefusals, isRevisionConflict } from "../lib/errors";
import { useStaffFormat } from "../lib/format";

/** What a preset writes itself (design S10, ftrl. § 8-23 and the IA agreement's extended egenmelding). */
const presets: Record<Exclude<StaffSelfCertPolicy, "custom">, { days: number; times?: number; perYear?: number }> = {
  minimum: { days: 3, times: 4 },
  extended: { days: 8, perYear: 24 },
};

interface PolicyValues {
  policy: StaffSelfCertPolicy;
  daysPerOccurrence: number | string;
  occurrencesPerYear: number | string;
  daysPerYear: number | string;
  afterMonths: number | string;
  autoCarryOver: boolean;
}

const valuesOf = (settings: StaffSettings): PolicyValues => ({
  policy: settings.selfCertPolicy,
  daysPerOccurrence: settings.selfCertDaysPerOccurrence,
  occurrencesPerYear: settings.selfCertOccurrencesPerYear,
  daysPerYear: settings.selfCertDaysPerYear,
  afterMonths: settings.selfCertAfterMonths,
  autoCarryOver: settings.autoCarryOver,
});

const whole = (value: number | string, min: number, max: number) =>
  value !== "" && Number.isInteger(Number(value)) && Number(value) >= min && Number(value) <= max;

const shownFields = [
  "selfCertPolicy",
  "selfCertDaysPerOccurrence",
  "selfCertOccurrencesPerYear",
  "selfCertDaysPerYear",
  "selfCertAfterMonths",
  "autoCarryOver",
];
const formField: Record<string, keyof PolicyValues> = {
  selfCertPolicy: "policy",
  selfCertDaysPerOccurrence: "daysPerOccurrence",
  selfCertOccurrencesPerYear: "occurrencesPerYear",
  selfCertDaysPerYear: "daysPerYear",
  selfCertAfterMonths: "afterMonths",
  autoCarryOver: "autoCarryOver",
};

/**
 * Settings' egenmelding policy and automatic carry-over (design S9, S10,
 * §3.6): the statutory minimum, the extended (IA) preset or custom numbers
 * — the form holding ftrl. § 8-23's floors too, the server staying the
 * judge — the months of employment before egenmelding, and whether untaken
 * holiday is carried over without HR's action.
 */
export const SelfCertPolicyForm = ({ settings }: { settings: StaffSettings }) => {
  const { t, date } = useStaffFormat();
  const queryClient = useQueryClient();
  const custom = (values: PolicyValues) => values.policy === "custom";
  const form = useForm<PolicyValues>({
    initialValues: valuesOf(settings),
    validate: {
      daysPerOccurrence: (value, values) => (!custom(values) || whole(value, 3, 16) ? null : t("selfCertDaysRange")),
      occurrencesPerYear: (value, values) =>
        !custom(values) || whole(value, 4, 52) ? null : t("selfCertOccurrencesRange"),
      daysPerYear: (value, values) => (!custom(values) || whole(value, 12, 365) ? null : t("selfCertDaysPerYearRange")),
      afterMonths: (value) => (whole(value, 0, 2) ? null : t("selfCertAfterMonthsRange")),
    },
  });

  // A pristine form follows the server, as the general settings do.
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
    mutationFn: (values: PolicyValues) =>
      updateStaffSettings(
        {
          ...generalSettingsOf(settings),
          selfCertPolicy: values.policy,
          // A preset writes its own numbers: they are sent under custom only.
          ...(custom(values)
            ? {
                selfCertDaysPerOccurrence: Number(values.daysPerOccurrence),
                selfCertOccurrencesPerYear: Number(values.occurrencesPerYear),
                selfCertDaysPerYear: Number(values.daysPerYear),
              }
            : {}),
          selfCertAfterMonths: Number(values.afterMonths),
          autoCarryOver: values.autoCarryOver,
          ...accountSettingsOf(settings),
          ...eraseSettingsOf(settings),
        },
        settings.revision,
      ),
    onSuccess: async () => {
      form.resetDirty();
      notifications.show({ color: "teal", title: t("settingsSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        form.resetDirty();
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, shownFields);
      form.setErrors(Object.fromEntries(Object.entries(fields).map(([field, words]) => [formField[field], words])));
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: rest.join(" ") });
    },
  });

  const preset = form.values.policy === "custom" ? undefined : presets[form.values.policy];
  return (
    <FieldGroup title={t("absenceSettings")} description={t("absenceSettingsDescription")}>
      <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
        <Stack>
          <Select
            label={t("selfCertPolicyField")}
            allowDeselect={false}
            data={(["minimum", "extended", "custom"] as const).map((value) => ({
              value,
              label: t(`selfCertPolicyOption_${value}`),
            }))}
            {...form.getInputProps("policy")}
          />
          {preset ? (
            <Text size="sm">
              {preset.times !== undefined
                ? t("selfCertPresetTimes", { days: preset.days, times: preset.times })
                : t("selfCertPresetDays", { days: preset.days, perYear: preset.perYear })}
            </Text>
          ) : (
            <SimpleGrid cols={{ base: 1, sm: 3 }}>
              <NumberInput
                label={t("selfCertDaysPerOccurrence")}
                allowDecimal={false}
                allowNegative={false}
                {...form.getInputProps("daysPerOccurrence")}
              />
              <NumberInput
                label={t("selfCertOccurrencesPerYear")}
                allowDecimal={false}
                allowNegative={false}
                {...form.getInputProps("occurrencesPerYear")}
              />
              <NumberInput
                label={t("selfCertDaysPerYear")}
                allowDecimal={false}
                allowNegative={false}
                {...form.getInputProps("daysPerYear")}
              />
            </SimpleGrid>
          )}
          <NumberInput
            label={t("selfCertAfterMonths")}
            description={t("selfCertAfterMonthsDescription")}
            allowDecimal={false}
            allowNegative={false}
            min={0}
            max={2}
            {...form.getInputProps("afterMonths")}
          />
          <Switch
            label={t("autoCarryOver")}
            description={
              settings.autoCarryOverSince
                ? t("autoCarryOverSince", { date: date(settings.autoCarryOverSince) })
                : t("autoCarryOverDescription")
            }
            {...form.getInputProps("autoCarryOver", { type: "checkbox" })}
          />
          <Group justify="flex-end">
            <Button type="submit" loading={save.isPending}>
              {t("save")}
            </Button>
          </Group>
        </Stack>
      </form>
    </FieldGroup>
  );
};
