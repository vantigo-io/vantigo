import { ActionIcon, Button, Group, Select, Stack, Table, Text } from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import type { ManufacturingInterval, ManufacturingWeekPattern } from "../api/work-centers";
import { useManufacturingFormat } from "../lib/format";
import {
  endTimes,
  MAX_INTERVALS_PER_DAY,
  nextInterval,
  openMinutes,
  type PatternIssue,
  startTimes,
  weekdays,
} from "../lib/pattern";

export interface PatternEditorProps {
  value: ManufacturingWeekPattern;
  onChange: (pattern: ManufacturingWeekPattern) => void;
  /** The problems to mark, from `patternIssues`. */
  issues: readonly PatternIssue[];
  readOnly?: boolean;
}

const startOptions = startTimes.map((time) => ({ value: time, label: time }));
const endOptions = endTimes.map((time) => ({ value: time, label: time }));

/**
 * The weekly working pattern (M9): each weekday's open intervals, at most
 * two, chosen on the 15-minute grid. A day without intervals is closed.
 * The problems `patternIssues` finds are marked on their interval, so the
 * person sees what the server would refuse before saving.
 */
export const PatternEditor = ({ value, onChange, issues, readOnly }: PatternEditorProps) => {
  const { t, minutes } = useManufacturingFormat();
  const setDay = (day: (typeof weekdays)[number], intervals: ManufacturingInterval[]) =>
    onChange({ ...value, [day]: intervals });

  return (
    <Table data-assist="manufacturing.work-center.pattern" verticalSpacing="xs">
      <Table.Thead>
        <Table.Tr>
          <Table.Th>{t("weekday")}</Table.Th>
          <Table.Th>{t("openIntervals")}</Table.Th>
          <Table.Th>{t("openHours")}</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {weekdays.map((day) => {
          const intervals = value[day] ?? [];
          const dayIssue = issues.find((issue) => issue.day === day && issue.index === undefined);
          return (
            <Table.Tr key={day}>
              <Table.Td>{t(`weekday_${day}`)}</Table.Td>
              <Table.Td>
                <Stack gap={4}>
                  {intervals.length === 0 && (
                    <Text size="sm" c="dimmed">
                      {t("closedDay")}
                    </Text>
                  )}
                  {intervals.map((interval, index) => {
                    const issue = issues.find((one) => one.day === day && one.index === index);
                    const set = (patch: Partial<ManufacturingInterval>) =>
                      setDay(
                        day,
                        intervals.map((one, i) => (i === index ? { ...one, ...patch } : one)),
                      );
                    return (
                      <Stack key={index} gap={2}>
                        <Group gap="xs" wrap="nowrap">
                          <Select
                            aria-label={t("intervalStart", { day: t(`weekday_${day}`), number: index + 1 })}
                            data={startOptions}
                            value={interval.start}
                            allowDeselect={false}
                            searchable
                            disabled={readOnly}
                            w={110}
                            error={issue ? true : undefined}
                            onChange={(next) => next && set({ start: next })}
                          />
                          <Text size="sm">–</Text>
                          <Select
                            aria-label={t("intervalEnd", { day: t(`weekday_${day}`), number: index + 1 })}
                            data={endOptions}
                            value={interval.end}
                            allowDeselect={false}
                            searchable
                            disabled={readOnly}
                            w={110}
                            error={issue ? true : undefined}
                            onChange={(next) => next && set({ end: next })}
                          />
                          {!readOnly && (
                            <ActionIcon
                              variant="subtle"
                              color="red"
                              aria-label={t("removeInterval", { day: t(`weekday_${day}`), number: index + 1 })}
                              onClick={() =>
                                setDay(
                                  day,
                                  intervals.filter((_, i) => i !== index),
                                )
                              }
                            >
                              <IconTrash size={16} />
                            </ActionIcon>
                          )}
                        </Group>
                        {issue && (
                          <Text size="xs" c="red">
                            {t(`patternProblem_${issue.problem}`)}
                          </Text>
                        )}
                      </Stack>
                    );
                  })}
                  {dayIssue && (
                    <Text size="xs" c="red">
                      {t(`patternProblem_${dayIssue.problem}`)}
                    </Text>
                  )}
                  {!readOnly && intervals.length < MAX_INTERVALS_PER_DAY && (
                    <Group>
                      <Button
                        variant="subtle"
                        size="compact-sm"
                        leftSection={<IconPlus size={14} />}
                        aria-label={t("addIntervalFor", { day: t(`weekday_${day}`) })}
                        onClick={() => setDay(day, [...intervals, nextInterval(intervals)])}
                      >
                        {t("addInterval")}
                      </Button>
                    </Group>
                  )}
                </Stack>
              </Table.Td>
              <Table.Td>{minutes(openMinutes(intervals))}</Table.Td>
            </Table.Tr>
          );
        })}
      </Table.Tbody>
    </Table>
  );
};
