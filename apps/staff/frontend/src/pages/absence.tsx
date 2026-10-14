import { ActionIcon, Alert, Box, Group, Select, Stack, Table, Tabs, Text } from "@mantine/core";
import { IconAlertCircle, IconChevronLeft, IconChevronRight } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageTabs, useI18n, useLocale } from "@vantigo/frontend-shell";
import { calendarQueryOptions, type StaffAbsence, type StaffCalendarPerson } from "../api/absence";
import { type StaffAbsenceType, type StaffStructure, staffMetaQueryOptions } from "../api/meta";
import { ApprovalsInbox } from "../components/approvals";
import "../i18n";
import { absenceDays, absenceName } from "../lib/absence";
import { fromUtc, toUtc } from "../lib/dates";
import { useStaffFormat } from "../lib/format";
import type { AbsenceSearch, AbsenceTab } from "../lib/search";
import { monthOf } from "../lib/workdays";

type Navigate = (options: { search: (previous: AbsenceSearch) => AbsenceSearch; replace?: boolean }) => void;

/**
 * Absence (design §7, S28): the calendar — a month, people as rows and their
 * absences as bars exactly as `GET /calendar` shaped them for the caller —
 * and, for a caller who decides anything, the approvals inbox. The page
 * names nothing the server did not send: a colleague's bar is "absent", an
 * HR-only type the reader may not name is "leave", and a type, a share of
 * the day or hours appear only on a row that carries them. The public
 * holidays are the server's (`publicHolidays`), never worked out here.
 */
export const AbsencePage = () => {
  const { t } = useI18n("staff");
  const search = useSearch({ strict: false }) as AbsenceSearch;
  const navigate = useNavigate() as unknown as Navigate;
  const meta = useQuery(staffMetaQueryOptions());

  const header = <PageHeader title={t("navAbsence")} description={t("absencePageDescription")} />;
  if (meta.isError)
    return (
      <Stack gap="lg">
        {header}
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadCalendar")}>
          {meta.error.message}
        </Alert>
      </Stack>
    );
  if (!meta.data) return <ContentSkeleton rows={6} rowHeight={44} />;
  const { capabilities } = meta.data;
  // Deciders by right or by the terms (canDecide), and staff:personal-data,
  // which alone decides the HR-only types (plan reading 33). What reaches the
  // inbox is the server's answer.
  const decides = capabilities.canDecide || capabilities.canSeeHealth;
  // The inbox first for a decider — the dashboard's approval items link here.
  const current: AbsenceTab = decides ? (search.tab ?? "approvals") : "calendar";

  return (
    <Stack gap="lg">
      {header}
      <PageTabs
        aria-label={t("absenceViews")}
        items={[
          { value: "calendar", label: t("absenceTab_calendar") },
          ...(decides ? [{ value: "approvals" as const, label: t("absenceTab_approvals") }] : []),
        ]}
        value={current}
        onChange={(next) => navigate({ search: (previous) => ({ ...previous, tab: next }), replace: true })}
      >
        <Tabs.Panel value="calendar" pt="lg">
          <AbsenceCalendar
            today={meta.data.today}
            month={search.month}
            departmentId={search.departmentId}
            structure={meta.data.structure}
            types={meta.data.absenceTypes}
            onChange={(next) => navigate({ search: (previous) => ({ ...previous, tab: "calendar", ...next }) })}
          />
        </Tabs.Panel>
        {decides && (
          <Tabs.Panel value="approvals" pt="lg">
            <ApprovalsInbox types={meta.data.absenceTypes} />
          </Tabs.Panel>
        )}
      </PageTabs>
    </Stack>
  );
};

/** The month `YYYY-MM` that is `offset` months from `month`. */
const shiftMonth = (month: string, offset: number): string =>
  fromUtc(new Date(Date.UTC(Number(month.slice(0, 4)), Number(month.slice(5, 7)) - 1 + offset, 1))).slice(0, 7);

/** Every day of a month, first to last. */
const daysOf = (from: string, to: string): string[] => {
  const days: string[] = [];
  for (let day = toUtc(from); fromUtc(day) <= to; day.setUTCDate(day.getUTCDate() + 1)) days.push(fromUtc(day));
  return days;
};

const DAY_WIDTH = 30;
const NAME_WIDTH = 160;

const AbsenceCalendar = ({
  today,
  month: wanted,
  departmentId,
  structure,
  types,
  onChange,
}: {
  today: string;
  month?: string;
  departmentId?: number;
  structure?: StaffStructure;
  types: readonly StaffAbsenceType[];
  onChange: (next: Pick<AbsenceSearch, "month" | "departmentId">) => void;
}) => {
  const { t } = useStaffFormat();
  const { formatters } = useI18n("staff");
  const month = wanted ?? today.slice(0, 7);
  // A month is at most 31 days: one read, well within GET /calendar's 93.
  const { from, to } = monthOf(`${month}-01`);
  const calendar = useQuery(calendarQueryOptions(from, to, departmentId));
  const title = formatters.formatDate(toUtc(from), { month: "long", year: "numeric", timeZone: "UTC" });
  // Only a reader of the structure (staff:view, staff:manage) names a department.
  const departments = structure?.departments.filter((one) => !one.archivedAt || one.id === departmentId) ?? [];

  return (
    <Stack gap="md">
      <Group justify="space-between" wrap="wrap" gap="sm">
        <Group gap="xs" wrap="nowrap">
          <ActionIcon
            variant="default"
            size="lg"
            aria-label={t("previousMonth")}
            onClick={() => onChange({ month: shiftMonth(month, -1) })}
          >
            <IconChevronLeft size={18} />
          </ActionIcon>
          <Text fw={600} miw={140} ta="center" aria-live="polite">
            {title}
          </Text>
          <ActionIcon
            variant="default"
            size="lg"
            aria-label={t("nextMonth")}
            onClick={() => onChange({ month: shiftMonth(month, 1) })}
          >
            <IconChevronRight size={18} />
          </ActionIcon>
        </Group>
        {departments.length > 0 && (
          <Select
            label={t("department")}
            data={[
              { value: "", label: t("allDepartments") },
              ...departments.map((one) => ({ value: String(one.id), label: one.name })),
            ]}
            value={departmentId === undefined ? "" : String(departmentId)}
            onChange={(value) => onChange({ departmentId: value ? Number(value) : undefined })}
            allowDeselect={false}
            w={{ base: "100%", xs: 240 }}
          />
        )}
      </Group>
      <Text size="sm" c="dimmed">
        {t("calendarLegend")}
      </Text>
      {calendar.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadCalendar")}>
          {calendar.error.message}
        </Alert>
      )}
      {calendar.isPending && <ContentSkeleton rows={4} rowHeight={32} />}
      {calendar.data && calendar.data.people.length === 0 && <EmptyState title={t("calendarEmpty")} />}
      {calendar.data && calendar.data.people.length > 0 && (
        <MonthTable
          title={t("calendarTable", { month: title })}
          days={daysOf(from, to)}
          holidays={calendar.data.publicHolidays}
          people={calendar.data.people}
          types={types}
        />
      )}
    </Stack>
  );
};

/**
 * The month as a table: a column per day — the weekends and the server's
 * public holidays shaded — and a row per person, whose absences are bars
 * laid on a grid of the same columns. On a phone the table scrolls inside
 * its own box, the person's name staying put.
 */
const MonthTable = ({
  title,
  days,
  holidays,
  people,
  types,
}: {
  title: string;
  days: string[];
  holidays: readonly string[];
  people: StaffCalendarPerson[];
  types: readonly StaffAbsenceType[];
}) => {
  const { t } = useStaffFormat();
  const { formatters } = useI18n("staff");
  const closed = (day: string): "holiday" | "weekend" | undefined => {
    if (holidays.includes(day)) return "holiday";
    const weekday = toUtc(day).getUTCDay();
    return weekday === 0 || weekday === 6 ? "weekend" : undefined;
  };
  const shade = (day: string) =>
    closed(day) === "holiday"
      ? "var(--mantine-color-orange-light)"
      : closed(day) === "weekend"
        ? "var(--mantine-color-default-hover)"
        : undefined;
  const columns = `repeat(${days.length}, minmax(0, 1fr))`;

  return (
    <Table.ScrollContainer minWidth={NAME_WIDTH + days.length * DAY_WIDTH}>
      <Table aria-label={title} withTableBorder layout="fixed" verticalSpacing={4} horizontalSpacing={0}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th
              w={NAME_WIDTH}
              px="xs"
              style={{ position: "sticky", left: 0, zIndex: 1, background: "var(--mantine-color-body)" }}
            >
              {t("calendarPerson")}
            </Table.Th>
            {days.map((day) => {
              const full = formatters.formatDate(toUtc(day), {
                weekday: "short",
                month: "short",
                day: "numeric",
                year: "numeric",
                timeZone: "UTC",
              });
              return (
                <Table.Th
                  key={day}
                  scope="col"
                  w={DAY_WIDTH}
                  ta="center"
                  aria-label={closed(day) === "holiday" ? t("publicHolidayDay", { date: full }) : full}
                  data-closed={closed(day)}
                  title={closed(day) === "holiday" ? t("publicHolidayDay", { date: full }) : full}
                  style={{ background: shade(day), fontWeight: 500 }}
                >
                  <Text span size="xs" aria-hidden>
                    {Number(day.slice(8))}
                  </Text>
                </Table.Th>
              );
            })}
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {people.map(({ person, absences }) => (
            <Table.Tr key={person.id}>
              <Table.Th
                scope="row"
                px="xs"
                style={{ position: "sticky", left: 0, zIndex: 1, background: "var(--mantine-color-body)" }}
              >
                <Text size="sm" fw={500} truncate>
                  {person.displayName}
                </Text>
              </Table.Th>
              <Table.Td colSpan={days.length} p={0} style={{ position: "relative" }}>
                <Box
                  aria-hidden
                  style={{ position: "absolute", inset: 0, display: "grid", gridTemplateColumns: columns }}
                >
                  {days.map((day) => (
                    <Box key={day} style={{ background: shade(day) }} />
                  ))}
                </Box>
                <Box
                  component="ul"
                  aria-label={person.displayName}
                  m={0}
                  py={4}
                  px={0}
                  style={{
                    position: "relative",
                    listStyle: "none",
                    display: "grid",
                    gridTemplateColumns: columns,
                    gridAutoRows: 22,
                    rowGap: 2,
                    minHeight: 30,
                  }}
                >
                  {absences.map((absence) => (
                    <AbsenceBar
                      key={absence.id ?? `${absence.firstDay}/${absence.lastDay}`}
                      absence={absence}
                      person={person.displayName}
                      days={days}
                      types={types}
                    />
                  ))}
                </Box>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

/** One absence's bar, clipped to the month: what the row carries, read out in full to a screen reader. */
const AbsenceBar = ({
  absence,
  person,
  days,
  types,
}: {
  absence: StaffAbsence;
  person: string;
  days: string[];
  types: readonly StaffAbsenceType[];
}) => {
  const { t, date, number } = useStaffFormat();
  const locale = useLocale();
  const name = absenceName(absence, types, locale, t);
  const label = [
    name,
    absenceDays(absence, date),
    ...(absence.status ? [t(`absenceStatus_${absence.status}`)] : []),
    ...(absence.percent !== undefined && absence.percent < 100
      ? [t("absencePercent", { percent: number(absence.percent) })]
      : []),
    ...(absence.hours !== undefined ? [t("absenceHours", { hours: number(absence.hours) })] : []),
    ...(absence.degreePercent !== undefined ? [t("absenceDegree", { percent: number(absence.degreePercent) })] : []),
  ].join(" · ");
  // The server answers rows that touch the range; one that does not is drawn nowhere.
  if (absence.lastDay < days[0] || absence.firstDay > days[days.length - 1]) return null;
  const start = Math.max(0, days.indexOf(absence.firstDay < days[0] ? days[0] : absence.firstDay));
  const lastIndex = days.indexOf(absence.lastDay > days[days.length - 1] ? days[days.length - 1] : absence.lastDay);
  const end = lastIndex < 0 ? days.length - 1 : lastIndex;
  const color = absence.shownAs === "type" ? "blue" : absence.shownAs === "leave" ? "grape" : "gray";
  const waiting = absence.status === "requested";

  return (
    <Box
      component="li"
      aria-label={`${person}: ${label}`}
      title={label}
      px={4}
      style={{
        gridColumn: `${start + 1} / ${end + 2}`,
        borderRadius: 4,
        overflow: "hidden",
        whiteSpace: "nowrap",
        textOverflow: "ellipsis",
        fontSize: "var(--mantine-font-size-xs)",
        lineHeight: "20px",
        background: waiting ? "transparent" : `var(--mantine-color-${color}-light)`,
        color: `var(--mantine-color-${color}-light-color)`,
        border: `1px ${waiting ? "dashed" : "solid"} var(--mantine-color-${color}-outline)`,
      }}
    >
      {name}
    </Box>
  );
};
