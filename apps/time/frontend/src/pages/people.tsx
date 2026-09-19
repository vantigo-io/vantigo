import { Alert, Badge, Card, Group, Select, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconLock } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { PEOPLE_DEFAULT_WEEKS, peopleOverviewQueryOptions, type TimePersonWeek } from "../api/people";
import type { ApiError } from "../api/request";
import "../i18n";
import { useHoursFormat } from "../lib/hours";

/** The page's URL search params, which the host route validates (Task 7). */
export interface PeopleSearch {
  /** How many weeks back the table reads, 1 to 12; absent is four. */
  weeks?: number;
}

/** The window the API takes: between one and twelve weeks. */
const MIN_WEEKS = 1;
const MAX_WEEKS = 12;

const windowOf = (weeks: number | undefined): number => {
  if (weeks === undefined || !Number.isInteger(weeks)) return PEOPLE_DEFAULT_WEEKS;
  return Math.min(Math.max(weeks, MIN_WEEKS), MAX_WEEKS);
};

/**
 * The people overview (design §8, `time:view-all`): everyone who logged time
 * in the last weeks, a column per week, with the hours and how far each week
 * has come. A caller without the permission is told so rather than shown an
 * error: the page is reachable from navigation before anyone knows.
 */
export const PeoplePage = () => {
  const { t, formatters } = useI18n("time");
  const { weeks } = useSearch({ strict: false }) as PeopleSearch;
  const navigate = useNavigate() as (options: unknown) => void;

  const weeksShown = windowOf(weeks);
  const { data, isPending, isError, error } = useQuery(peopleOverviewQueryOptions(weeksShown));
  const forbidden = (error as ApiError | null)?.status === 403;

  const weekStarts = data?.[0]?.weeks.map((week) => week.weekStart) ?? [];
  const options = Array.from({ length: MAX_WEEKS }, (_, i) => ({
    value: String(i + 1),
    label: i === 0 ? t("oneWeekOption") : t("weeksOption", { count: i + 1 }),
  }));

  return (
    <Stack gap="lg">
      <PageHeader title={t("people")} description={t("peopleDescription")} />

      {/* Nothing to narrow when the caller may not see the table at all. */}
      {!forbidden && (
        <Group>
          <Select
            label={t("weeksShown")}
            data={options}
            value={String(weeksShown)}
            allowDeselect={false}
            w={160}
            onChange={(value) => navigate({ search: { weeks: Number(value) } })}
          />
        </Group>
      )}

      <Card withBorder padding="lg" radius="md">
        {isError && !forbidden && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadPeople")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={4} rowHeight={48} />}

        {forbidden && (
          <EmptyState icon={IconLock} title={t("peopleForbidden")} description={t("peopleForbiddenDescription")} />
        )}

        {data && data.length === 0 && <EmptyState title={t("noPeople")} description={t("noPeopleDescription")} />}

        {data && data.length > 0 && (
          <Table.ScrollContainer minWidth={220 + weekStarts.length * 140}>
            <Table striped highlightOnHover verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("person")}</Table.Th>
                  {weekStarts.map((weekStart) => (
                    <Table.Th key={weekStart}>
                      {formatters.formatDate(weekStart, { day: "numeric", month: "short", timeZone: "UTC" })}
                    </Table.Th>
                  ))}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {data.map((person) => (
                  <Table.Tr key={person.userId}>
                    <Table.Td>
                      <Text size="sm" fw={600}>
                        {person.displayName}
                      </Text>
                    </Table.Td>
                    {person.weeks.map((week) => (
                      <Table.Td key={week.weekStart}>
                        <WeekCell week={week} />
                      </Table.Td>
                    ))}
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Card>
    </Stack>
  );
};

/** One person's week: the hours, and the chips that say how far it has come. */
const WeekCell = ({ week }: { week: TimePersonWeek }) => {
  const { t } = useI18n("time");
  const hours = useHoursFormat();
  return (
    <Stack gap={4} align="flex-start">
      <Text size="sm" fw={week.hours > 0 ? 600 : 400} c={week.hours > 0 ? undefined : "dimmed"}>
        {hours.display(week.hours)}
      </Text>
      {week.submittedAt && (
        <Badge variant="light" color="blue" size="sm">
          {t("statusSubmitted")}
        </Badge>
      )}
      {week.approvedHours > 0 && (
        <Badge variant="light" color="green" size="sm">
          {t("approvedHours", { hours: hours.display(week.approvedHours) })}
        </Badge>
      )}
      {week.rejectedCount > 0 && (
        <Badge variant="light" color="red" size="sm">
          {t("rejectedCount", { count: week.rejectedCount })}
        </Badge>
      )}
    </Stack>
  );
};
