import { Alert, Button, Group, NumberInput, SimpleGrid, Stack, Text } from "@mantine/core";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { STAFF_QUERY_KEY } from "../api/request";
import { runRetention, type StaffRetentionRun } from "../api/retention";
import { type StaffSettings, updateStaffSettings } from "../api/settings";
import { FieldGroup, Fields } from "../components/field-group";
import "../i18n";
import { fieldRefusals, isRevisionConflict } from "../lib/errors";

interface PeriodValues {
  retentionGraceDays: number | string;
  healthRetentionDays: number | string;
}

const periodsOf = (settings: StaffSettings): PeriodValues => ({
  retentionGraceDays: settings.retentionGraceDays,
  healthRetentionDays: settings.healthRetentionDays,
});

/** A whole number of days within the bounds `PUT /settings` takes. */
const withinDays = (value: number | string, min: number) => {
  const days = Number(value);
  return value !== "" && Number.isInteger(days) && days >= min && days <= 3650;
};

/**
 * Retention in Settings (design S15), `staff:manage`'s: the two periods,
 * saved through `PUT /settings` under the revision read — the replace carries
 * the general settings as read, since every field but the periods left out
 * is cleared — the derived block, read-only, and **Run retention now** after
 * a confirm, its result said in words; `retention_busy` while the worker
 * holds the lease.
 */
export const RetentionPanel = ({ settings }: { settings: StaffSettings }) => {
  const { t, formatters } = useI18n("staff");
  const queryClient = useQueryClient();
  const [result, setResult] = useState<StaffRetentionRun | null>(null);

  const form = useForm<PeriodValues>({
    initialValues: periodsOf(settings),
    validate: {
      retentionGraceDays: (value) => (withinDays(value, 0) ? null : t("retentionGraceInvalid")),
      healthRetentionDays: (value) => (withinDays(value, 30) ? null : t("healthRetentionInvalid")),
    },
  });

  // A pristine form follows the server, as the general form does — keyed on
  // the whole row, so a conflict over a general field (the periods
  // unchanged) still reloads the periods this form had typed over.
  const loaded = JSON.stringify(settings);
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty()) {
      form.setInitialValues(periodsOf(settings));
      form.setValues(periodsOf(settings));
    }
  }

  const save = useMutation({
    mutationFn: (values: PeriodValues) =>
      updateStaffSettings(
        {
          timeZone: settings.timeZone,
          ...(settings.employeeNumberPrefix ? { employeeNumberPrefix: settings.employeeNumberPrefix } : {}),
          nextEmployeeNumber: settings.nextEmployeeNumber,
          ...(settings.pensionProvider ? { pensionProvider: settings.pensionProvider } : {}),
          ...(settings.injuryInsurer ? { injuryInsurer: settings.injuryInsurer } : {}),
          retentionGraceDays: Number(values.retentionGraceDays),
          healthRetentionDays: Number(values.healthRetentionDays),
        },
        settings.revision,
      ),
    onSuccess: async () => {
      form.resetDirty();
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("periodsSaved"), message: "" });
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        form.resetDirty();
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, ["retentionGraceDays", "healthRetentionDays"]);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: rest.join(" ") });
    },
  });

  const run = useMutation({
    mutationFn: runRetention,
    onSuccess: async (answer) => {
      setResult(answer);
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({
        color: (error as { code?: string }).code === "retention_busy" ? "yellow" : "red",
        title: t("couldNotRunRetention"),
        message: (error as { code?: string }).code === "retention_busy" ? t("retentionBusy") : error.message,
      }),
  });

  const confirmRun = () =>
    modals.openConfirmModal({
      title: t("runRetentionTitle"),
      children: <Text size="sm">{t("runRetentionConfirm")}</Text>,
      labels: { confirm: t("run"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => {
        setResult(null);
        run.mutate();
      },
    });

  const at = (value: string | undefined) =>
    value ? formatters.formatDate(new Date(value), { dateStyle: "medium" }) : t("never");
  const { retention } = settings;

  const said = (answer: StaffRetentionRun) => {
    const kinds = answer.erased.kinds.map(({ kind, count }) =>
      t("erasedKindCount", { kind: t(`erasedKind_${kind}`), count: formatters.formatNumber(count) }),
    );
    const erased = kinds.length > 0 ? t("retentionRunErased", { kinds: kinds.join(", ") }) : t("retentionRunNothing");
    const deletions = t("retentionRunDeletions", {
      attempted: formatters.formatNumber(answer.deletions.attempted),
      succeeded: formatters.formatNumber(answer.deletions.succeeded),
    });
    return `${erased} ${deletions}`;
  };

  return (
    <FieldGroup title={t("retentionTitle")} description={t("retentionDescription")}>
      <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
        <Stack gap="sm">
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <NumberInput
              label={t("retentionGraceDays")}
              description={t("retentionGraceDaysDescription")}
              withAsterisk
              allowDecimal={false}
              allowNegative={false}
              thousandSeparator={false}
              {...form.getInputProps("retentionGraceDays")}
            />
            <NumberInput
              label={t("healthRetentionDays")}
              description={t("healthRetentionDaysDescription")}
              withAsterisk
              allowDecimal={false}
              allowNegative={false}
              thousandSeparator={false}
              {...form.getInputProps("healthRetentionDays")}
            />
          </SimpleGrid>
          <Group justify="flex-end">
            <Button type="submit" variant="light" loading={save.isPending}>
              {t("savePeriods")}
            </Button>
          </Group>
        </Stack>
      </form>
      <Fields
        items={[
          [t("pendingDeletions"), formatters.formatNumber(retention.pendingDeletions)],
          [t("failingDeletions"), formatters.formatNumber(retention.failingDeletions)],
          [t("oldestPendingSince"), retention.oldestPendingSince ? at(retention.oldestPendingSince) : undefined],
          [t("lastErasedAt"), at(retention.lastErasedAt)],
        ]}
      />
      {retention.failingDeletions > 0 && (
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
          {t("failingDeletionsHint")}
        </Alert>
      )}
      <Text size="sm" c="dimmed">
        {t("runRetentionExplained")}
      </Text>
      <Group>
        <Button color="red" variant="light" loading={run.isPending} onClick={confirmRun}>
          {t("runRetentionNow")}
        </Button>
      </Group>
      {result && (
        <Text size="sm" role="status">
          {said(result)}
        </Text>
      )}
    </FieldGroup>
  );
};
