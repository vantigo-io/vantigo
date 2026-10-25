import { Button, Group, NumberInput, Stack } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
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

interface ReviewValues {
  interval: number | string;
}

const valuesOf = (settings: StaffSettings): ReviewValues => ({ interval: settings.competenceReviewIntervalMonths });

const whole = (value: number | string, min: number, max: number) =>
  value !== "" && Number.isInteger(Number(value)) && Number(value) >= min && Number(value) <= max;

/**
 * Settings' competence review interval (staff competence design §3.9, S11):
 * the months within which a manager goes through a person's competences
 * with them before the review is overdue — 0 turns the item off. Sent as
 * the egenmelding policy sends its fields: the general settings as read,
 * this field, the account and erase settings kept, under the settings'
 * revision; every other form leaves the field out, and the server keeps it.
 */
export const CompetenceReviewSettings = ({ settings }: { settings: StaffSettings }) => {
  const { t } = useStaffFormat();
  const queryClient = useQueryClient();
  const form = useForm<ReviewValues>({
    initialValues: valuesOf(settings),
    validate: { interval: (value) => (whole(value, 0, 60) ? null : t("competenceReviewIntervalRange")) },
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
    mutationFn: (values: ReviewValues) =>
      updateStaffSettings(
        {
          ...generalSettingsOf(settings),
          competenceReviewIntervalMonths: Number(values.interval),
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
      const { fields, rest } = fieldRefusals(error, ["competenceReviewIntervalMonths"]);
      if (fields.competenceReviewIntervalMonths) form.setErrors({ interval: fields.competenceReviewIntervalMonths });
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: rest.join(" ") });
    },
  });

  return (
    <FieldGroup title={t("competenceReviewSettings")} description={t("competenceReviewSettingsDescription")}>
      <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
        <Stack>
          <NumberInput
            label={t("competenceReviewInterval")}
            description={t("competenceReviewIntervalDescription")}
            allowDecimal={false}
            allowNegative={false}
            min={0}
            max={60}
            {...form.getInputProps("interval")}
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
