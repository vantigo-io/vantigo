import { Chip, Group, NumberInput, SegmentedControl, Select, Stack, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useI18n } from "@vantigo/frontend-shell";
import type { JSX } from "react";
import "../i18n";
import {
  type RecurrenceEnd,
  type RecurrenceFrequency,
  type RecurrenceModel,
  type Weekday,
  type WeekdayOrdinal,
  weekdays,
} from "../lib/recurrence";

export interface RecurrenceControlProps {
  value: RecurrenceModel;
  onChange: (value: RecurrenceModel) => void;
  /** The server's field error on `recurrence.rule`. */
  error?: string;
  disabled?: boolean;
}

const weekdayKeys: Record<Weekday, string> = {
  MO: "weekdayMo",
  TU: "weekdayTu",
  WE: "weekdayWe",
  TH: "weekdayTh",
  FR: "weekdayFr",
  SA: "weekdaySa",
  SU: "weekdaySu",
};

const ordinalKeys: Record<string, string> = {
  "1": "ordinalFirst",
  "2": "ordinalSecond",
  "3": "ordinalThird",
  "4": "ordinalFourth",
  "-1": "ordinalLast",
};

/**
 * How an appointment repeats (design D4, D8): not at all, daily, weekly on
 * chosen days or monthly on the day or an ordinal weekday, every n, until a
 * date or n times. It edits the model `lib/recurrence.ts` writes to the rule
 * and reads back from it.
 */
export function RecurrenceControl({ value, onChange, error, disabled }: RecurrenceControlProps): JSX.Element {
  const { t } = useI18n("calendar");
  const set = (patch: Partial<RecurrenceModel>) => onChange({ ...value, ...patch });
  const unitKey =
    value.freq === "daily" ? "intervalDays" : value.freq === "weekly" ? "intervalWeeks" : "intervalMonths";

  return (
    <Stack gap="xs">
      <Select
        label={t("repeat")}
        disabled={disabled}
        allowDeselect={false}
        value={value.freq}
        onChange={(freq) => freq && set({ freq: freq as RecurrenceFrequency })}
        data={[
          { value: "none", label: t("repeatNone") },
          { value: "daily", label: t("repeatDaily") },
          { value: "weekly", label: t("repeatWeekly") },
          { value: "monthly", label: t("repeatMonthly") },
        ]}
        error={value.freq === "none" ? error : undefined}
      />
      {value.freq !== "none" && (
        <>
          <Group gap="xs" align="flex-end" wrap="nowrap">
            <NumberInput
              label={t("repeatEvery")}
              disabled={disabled}
              min={1}
              max={99}
              allowDecimal={false}
              allowNegative={false}
              w={110}
              value={value.interval}
              onChange={(interval) => set({ interval: Number(interval) || 1 })}
            />
            <Text size="sm" pb={8}>
              {t(unitKey, { count: value.interval })}
            </Text>
          </Group>
          {value.freq === "weekly" && (
            <Chip.Group
              multiple
              value={value.weekdays}
              onChange={(days) => set({ weekdays: weekdays.filter((day) => days.includes(day)) })}
            >
              <Group gap={6} aria-label={t("repeatOn")} role="group">
                {weekdays.map((day) => (
                  <Chip key={day} value={day} size="xs" disabled={disabled}>
                    {t(weekdayKeys[day])}
                  </Chip>
                ))}
              </Group>
            </Chip.Group>
          )}
          {value.freq === "monthly" && (
            <Stack gap="xs">
              <SegmentedControl
                aria-label={t("monthlyOn")}
                disabled={disabled}
                value={value.monthlyMode}
                onChange={(mode) => set({ monthlyMode: mode as RecurrenceModel["monthlyMode"] })}
                data={[
                  { value: "day", label: t("monthlyOnDay", { day: value.monthDay }) },
                  {
                    value: "weekday",
                    label: t("monthlyOnWeekday", {
                      ordinal: t(ordinalKeys[String(value.ordinal)]),
                      weekday: t(weekdayKeys[value.ordinalWeekday]),
                    }),
                  },
                ]}
              />
              {value.monthlyMode === "weekday" && (
                <Group gap="xs" grow>
                  <Select
                    aria-label={t("ordinal")}
                    disabled={disabled}
                    allowDeselect={false}
                    value={String(value.ordinal)}
                    onChange={(ordinal) => ordinal && set({ ordinal: Number(ordinal) as WeekdayOrdinal })}
                    data={["1", "2", "3", "4", "-1"].map((ordinal) => ({
                      value: ordinal,
                      label: t(ordinalKeys[ordinal]),
                    }))}
                  />
                  <Select
                    aria-label={t("weekday")}
                    disabled={disabled}
                    allowDeselect={false}
                    value={value.ordinalWeekday}
                    onChange={(day) => day && set({ ordinalWeekday: day as Weekday })}
                    data={weekdays.map((day) => ({ value: day, label: t(weekdayKeys[day]) }))}
                  />
                </Group>
              )}
              {value.monthlyMode === "day" && (
                <NumberInput
                  label={t("monthDay")}
                  disabled={disabled}
                  min={1}
                  max={31}
                  allowDecimal={false}
                  allowNegative={false}
                  w={110}
                  value={value.monthDay}
                  onChange={(day) => set({ monthDay: Math.min(31, Math.max(1, Number(day) || 1)) })}
                />
              )}
            </Stack>
          )}
          <SegmentedControl
            aria-label={t("repeatEnds")}
            disabled={disabled}
            value={value.end}
            onChange={(end) => set({ end: end as RecurrenceEnd })}
            data={[
              { value: "none", label: t("endsNever") },
              { value: "until", label: t("endsOn") },
              { value: "count", label: t("endsAfter") },
            ]}
          />
          {value.end === "until" && (
            <DateInput
              label={t("untilDate")}
              disabled={disabled}
              valueFormat={t("dateInputFormat")}
              value={value.until}
              onChange={(until) => set({ until: until ? String(until).slice(0, 10) : null })}
            />
          )}
          {value.end === "count" && (
            <NumberInput
              label={t("occurrenceCount")}
              disabled={disabled}
              min={1}
              max={730}
              allowDecimal={false}
              allowNegative={false}
              w={140}
              value={value.count}
              onChange={(count) => set({ count: Number(count) || 1 })}
            />
          )}
          {error && (
            <Text size="xs" c="red" role="alert">
              {error}
            </Text>
          )}
        </>
      )}
    </Stack>
  );
}
