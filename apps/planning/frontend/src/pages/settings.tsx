import { Alert, Button, Card, Group, NumberInput, Select, SimpleGrid, Stack, Text } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import type { PlanningLoadUnit } from "../api/meta";
import { ApiValidationError, isRevisionConflict, PLANNING_QUERY_KEY } from "../api/request";
import {
  type PlanningOverbooking,
  type PlanningSettings,
  type PlanningSettingsInput,
  planningSettingsQueryOptions,
  updatePlanningSettings,
} from "../api/settings";
import { usePlanningFormat } from "../lib/format";

/**
 * Planning settings (design S20, §7): how overbooking is judged, the load
 * unit the sheet starts with, the unbooked list's threshold and horizon, and
 * the attention items' lead times. `planning:manage`'s.
 */
export const PlanningSettingsPage = () => {
  const { t } = usePlanningFormat();
  const settings = useQuery(planningSettingsQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader title={t("settingsTitle")} description={t("settingsDescription")} />
      {settings.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadSettings")}>
          {settings.error.message}
        </Alert>
      )}
      {settings.isPending && <ContentSkeleton rows={4} rowHeight={52} />}
      {settings.data && <SettingsForm key={settings.data.revision} settings={settings.data} />}
    </Stack>
  );
};

type Values = Required<PlanningSettingsInput>;

const fields = [
  "overbooking",
  "defaultLoadUnit",
  "unbookedThresholdPercent",
  "unbookedHorizonWeeks",
  "requestLeadDays",
  "endingLeadWeeks",
] as const;

const SettingsForm = ({ settings }: { settings: PlanningSettings }) => {
  const { t } = usePlanningFormat();
  const queryClient = useQueryClient();
  const between = (min: number, max: number) => (value: number) =>
    Number.isInteger(value) && value >= min && value <= max ? null : t("wholeNumberBetween", { min, max });
  const form = useForm<Values>({
    initialValues: Object.fromEntries(fields.map((field) => [field, settings[field]])) as Values,
    validate: {
      unbookedThresholdPercent: between(1, 100),
      unbookedHorizonWeeks: between(1, 52),
      requestLeadDays: between(0, 365),
      endingLeadWeeks: between(1, 26),
    },
  });
  const save = useMutation({
    mutationFn: (values: Values) => updatePlanningSettings(values, settings.revision),
    onSuccess: async (saved) => {
      queryClient.setQueryData(planningSettingsQueryOptions().queryKey, saved);
      notifications.show({ color: "teal", title: t("settingsSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
        return;
      }
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSave"), message: error.message });
    },
  });
  const number = (
    field: Exclude<(typeof fields)[number], "overbooking" | "defaultLoadUnit">,
    min: number,
    max: number,
  ) => (
    <NumberInput
      label={t(`setting_${field}`)}
      description={t(`setting_${field}Description`)}
      min={min}
      max={max}
      clampBehavior="none"
      allowDecimal={false}
      allowNegative={false}
      {...form.getInputProps(field)}
    />
  );
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack gap="lg">
        <Card withBorder padding="lg" radius="md">
          <Stack>
            <Select
              label={t("setting_overbooking")}
              allowDeselect={false}
              data={(["warn", "refuse"] as PlanningOverbooking[]).map((value) => ({
                value,
                label: t(`overbooking_${value}`),
              }))}
              {...form.getInputProps("overbooking")}
            />
            <Text size="sm" c="dimmed">
              {t(`overbookingExplained_${form.values.overbooking}`)}
            </Text>
            <Select
              label={t("setting_defaultLoadUnit")}
              description={t("setting_defaultLoadUnitDescription")}
              allowDeselect={false}
              data={(["percent", "hours_per_week"] as PlanningLoadUnit[]).map((value) => ({
                value,
                label: t(`loadUnit_${value}`),
              }))}
              {...form.getInputProps("defaultLoadUnit")}
            />
          </Stack>
        </Card>
        <Card withBorder padding="lg" radius="md">
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            {number("unbookedThresholdPercent", 1, 100)}
            {number("unbookedHorizonWeeks", 1, 52)}
            {number("endingLeadWeeks", 1, 26)}
            {number("requestLeadDays", 0, 365)}
          </SimpleGrid>
        </Card>
        <Group justify="flex-end">
          <Button type="submit" loading={save.isPending}>
            {t("saveSettings")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
