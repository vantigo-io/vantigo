import {
  Alert,
  Button,
  Card,
  Checkbox,
  Group,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import dayjs from "dayjs";
import { useState } from "react";
import {
  type ReminderSettings,
  type ReminderSettingsInput,
  reminderSettingsQueryOptions,
  updateReminderSettings,
} from "../api/reminder-settings";
import { ApiConflictError, ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import { StaleAlert } from "../components/stale-alert";
import "../i18n";
import { useWho } from "../lib/bank";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

/** The settings' own fields, each of which has an input here. */
type Values = Omit<ReminderSettingsInput, "revision">;
const settingsInputs = new Set<string>([
  "enabled",
  "firstReminderDays",
  "deadlineDays",
  "graceDays",
  "remindersBeforeNotice",
  "collectionNotice",
  "personCharge",
  "businessCharge",
  "lateInterest",
  "staleImportDays",
  "inkassolov2026From",
  "regimeReviewedThrough",
]);

const valuesOf = (s: ReminderSettings): Values => ({
  enabled: s.enabled,
  firstReminderDays: s.firstReminderDays,
  deadlineDays: s.deadlineDays,
  graceDays: s.graceDays,
  remindersBeforeNotice: s.remindersBeforeNotice,
  collectionNotice: s.collectionNotice,
  personCharge: s.personCharge,
  businessCharge: s.businessCharge,
  lateInterest: s.lateInterest,
  staleImportDays: s.staleImportDays,
  inkassolov2026From: s.inkassolov2026From,
  regimeReviewedThrough: s.regimeReviewedThrough,
});

export interface ReminderSettingsCardProps {
  /** Today in Oslo, meta's: the review moves at most a year beyond it. */
  today: string;
  currentUserId?: string;
}

/**
 * The reminder settings (invoices payments and reminders design D7) and the
 * collection-law regime with its review (D6), each field with its bounds in
 * words. Saved whole with the revision it was read at; a save refused as
 * stale, or a newer revision seen while editing, says the settings changed and
 * offers Reload, as the seller form does. Changing anything changes only
 * letters made afterwards.
 */
export const ReminderSettingsCard = ({ today, currentUserId }: ReminderSettingsCardProps) => {
  const { t, date } = useInvoiceFormat();
  const settings = useQuery(reminderSettingsQueryOptions());
  const [editedFrom, setEditedFrom] = useState<ReminderSettings | null>(null);
  const shown = editedFrom ?? settings.data;
  return (
    <Card withBorder data-testid="reminder-settings">
      <Stack>
        <Title order={4}>{t("reminderSettings.title")}</Title>
        <Text size="sm" c="dimmed">
          {t("reminderSettings.description")}
        </Text>
        {settings.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("reminderSettings.couldNotLoad")}>
            {refusalMessage(settings.error, t, date)}
          </Alert>
        )}
        {settings.isPending && <ContentSkeleton rows={4} rowHeight={36} />}
        {shown && settings.data && (
          <ReminderSettingsForm
            key={shown.revision}
            settings={shown}
            latestRevision={settings.data.revision}
            dirty={shown === editedFrom}
            onDirtyChange={(dirty) => setEditedFrom((current) => (dirty ? (current ?? shown) : null))}
            today={today}
            currentUserId={currentUserId}
          />
        )}
      </Stack>
    </Card>
  );
};

interface FormProps {
  settings: ReminderSettings;
  latestRevision: number;
  dirty: boolean;
  onDirtyChange: (dirty: boolean) => void;
  today: string;
  currentUserId?: string;
}

const ReminderSettingsForm = ({
  settings,
  latestRevision,
  dirty,
  onDirtyChange: setDirty,
  today,
  currentUserId,
}: FormProps) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const queryClient = useQueryClient();
  const [values, setValues] = useState<Values>(() => valuesOf(settings));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [conflict, setConflict] = useState(false);
  const [reloadFailed, setReloadFailed] = useState(false);
  const stale = conflict || latestRevision > settings.revision;
  const set = <K extends keyof Values>(key: K, value: Values[K]) => {
    setValues((v) => ({ ...v, [key]: value }));
    setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== key)));
    setDirty(true);
  };
  const days = (
    key: "firstReminderDays" | "deadlineDays" | "graceDays" | "remindersBeforeNotice" | "staleImportDays",
    min: number,
    max: number,
  ) => ({
    label: t(`reminderSettings.${key}`),
    description: t(`reminderSettings.${key}Hint`),
    min,
    max,
    allowDecimal: false,
    value: values[key],
    error: errors[key],
    onChange: (v: number | string) => set(key, typeof v === "number" ? v : Number(v)),
  });
  const save = useMutation({
    mutationFn: () => updateReminderSettings({ ...values, revision: settings.revision }),
    onSuccess: async (saved) => {
      queryClient.setQueryData(reminderSettingsQueryOptions().queryKey, saved);
      setDirty(false);
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("saved") });
    },
    onError: (error) => {
      if (error instanceof ApiConflictError && !error.code) {
        setConflict(true);
        return;
      }
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(
          error,
          t,
          (field) => settingsInputs.has(field),
          "reminderSettings",
        );
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("reminderSettings.couldNotSave"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({
        color: "red",
        title: t("reminderSettings.couldNotSave"),
        message: refusalMessage(error, t, date),
      });
    },
  });
  const reload = useMutation({
    mutationFn: () => queryClient.fetchQuery({ ...reminderSettingsQueryOptions(), staleTime: 0 }),
    onMutate: () => setReloadFailed(false),
    onSuccess: () => setDirty(false),
    onError: () => setReloadFailed(true),
  });
  const reviewLimit = dayjs(today).add(1, "year").format("YYYY-MM-DD");
  return (
    <Stack>
      {stale && (
        <StaleAlert
          title={t("reminderSettings.changedTitle")}
          message={t("reminderSettings.changedMessage")}
          reloadFailedMessage={reloadFailed ? t("reminderSettings.couldNotReload") : undefined}
          reloading={reload.isPending}
          onReload={() => reload.mutate()}
        />
      )}
      <Checkbox
        label={t("reminderSettings.enabled")}
        description={t("reminderSettings.enabledHint")}
        checked={values.enabled}
        error={errors.enabled}
        onChange={(e) => set("enabled", e.currentTarget.checked)}
      />
      <SimpleGrid cols={{ base: 1, sm: 2 }}>
        <NumberInput {...days("firstReminderDays", 1, 60)} />
        <NumberInput {...days("deadlineDays", 14, 60)} />
        <NumberInput {...days("graceDays", 1, 10)} />
        <NumberInput {...days("remindersBeforeNotice", 0, 2)} />
      </SimpleGrid>
      <Checkbox
        label={t("reminderSettings.collectionNotice")}
        description={t("reminderSettings.collectionNoticeHint")}
        checked={values.collectionNotice}
        error={errors.collectionNotice}
        onChange={(e) => set("collectionNotice", e.currentTarget.checked)}
      />
      <SimpleGrid cols={{ base: 1, sm: 2 }}>
        <Select
          label={t("reminderSettings.personCharge")}
          description={t("reminderSettings.personChargeHint")}
          allowDeselect={false}
          data={(["fee", "none"] as const).map((v) => ({ value: v, label: t(`reminderSettings.charge.${v}`) }))}
          value={values.personCharge}
          error={errors.personCharge}
          onChange={(v) => v && set("personCharge", v as Values["personCharge"])}
        />
        <Select
          label={t("reminderSettings.businessCharge")}
          description={t("reminderSettings.businessChargeHint")}
          allowDeselect={false}
          data={(["fee", "compensation", "none"] as const).map((v) => ({
            value: v,
            label: t(`reminderSettings.charge.${v}`),
          }))}
          value={values.businessCharge}
          error={errors.businessCharge}
          onChange={(v) => v && set("businessCharge", v as Values["businessCharge"])}
        />
      </SimpleGrid>
      <Checkbox
        label={t("reminderSettings.lateInterest")}
        description={t("reminderSettings.lateInterestHint")}
        checked={values.lateInterest}
        error={errors.lateInterest}
        onChange={(e) => set("lateInterest", e.currentTarget.checked)}
      />
      <NumberInput {...days("staleImportDays", 1, 30)} />
      <Title order={5}>{t("reminderSettings.regime")}</Title>
      <Text size="sm">{t("reminderSettings.regimeExplain")}</Text>
      <SimpleGrid cols={{ base: 1, sm: 2 }}>
        <DateInput
          label={t("reminderSettings.inkassolov2026From")}
          description={t("reminderSettings.inkassolov2026FromHint")}
          valueFormat={t("dateInputFormat")}
          clearable
          value={values.inkassolov2026From}
          error={errors.inkassolov2026From}
          onChange={(v) => set("inkassolov2026From", v)}
        />
        <DateInput
          label={t("reminderSettings.regimeReviewedThrough")}
          description={t("reminderSettings.regimeReviewedThroughHint", { limit: date(reviewLimit) })}
          valueFormat={t("dateInputFormat")}
          maxDate={reviewLimit}
          value={values.regimeReviewedThrough}
          error={errors.regimeReviewedThrough}
          onChange={(v) => v && set("regimeReviewedThrough", v)}
        />
      </SimpleGrid>
      <Text size="xs" c="dimmed" data-testid="regime-reviewed">
        {settings.regimeReviewedBy
          ? t("reminderSettings.reviewedBy", {
              at: dateTime(settings.regimeReviewedAt),
              who: who(settings.regimeReviewedBy),
            })
          : t("reminderSettings.reviewedByRelease", { at: dateTime(settings.regimeReviewedAt) })}
      </Text>
      <Group justify="flex-end">
        <Button disabled={!dirty || save.isPending} loading={save.isPending} onClick={() => save.mutate()}>
          {t("save")}
        </Button>
      </Group>
    </Stack>
  );
};
