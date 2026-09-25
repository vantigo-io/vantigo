import { ActionIcon, Alert, Anchor, Badge, Button, Card, Group, Stack, Table, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconChevronLeft,
  IconChevronRight,
  IconListCheck,
  IconLock,
  IconPlus,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n, useShellLink } from "@vantigo/frontend-shell";
import { useState } from "react";
import { createTimeEntry, deleteTimeEntry, type TimeEntry, timeEntryUpdateFrom, updateTimeEntry } from "../api/entries";
import { isLoggable, myOpenTasksQueryOptions, myProjectsQueryOptions } from "../api/projects";
import { isLocked, type TimeSettings, timeSettingsQueryOptions } from "../api/settings";
import { submitWeek, type TimeWeek, type TimeWeekRow, weekQueryOptions } from "../api/weeks";
import { HoursCell } from "../components/hours-cell";
import { WorkTypeBadge } from "../components/work-type-badge";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useHoursFormat } from "../lib/hours";
import { rowKey, rowLabel, rowRef, type WeekRowRef } from "../lib/rows";
import { timeEntryStatusLabelKey } from "../lib/status";
import { addDays, dayUrl, isIsoDate, mondayOf, today, weekDays } from "../lib/week";
import { RowPicker } from "./-row-picker";

/**
 * The page's URL search params, which the host route validates (Task 7).
 * `week` is a Monday; any other date shows the week it falls in, and an
 * absent one the caller's current week.
 */
export interface MyWeekSearch {
  week?: string;
}

/** Plain calendar dates, formatted in UTC so a local evening never moves them a day. */
const useDayFormat = () => {
  const { formatters } = useI18n("time");
  return {
    short: (date: string) => formatters.formatDate(date, { weekday: "short", day: "numeric", timeZone: "UTC" }),
    long: (date: string) =>
      formatters.formatDate(date, { weekday: "long", day: "numeric", month: "long", timeZone: "UTC" }),
    medium: (date: string) => formatters.formatDate(date, { dateStyle: "medium", timeZone: "UTC" }),
  };
};

/**
 * My week (design §8): the caller's own hours for one week, a row per
 * project, line and task they log against and a column per day. Typing into
 * a day saves it; the week is submitted for approval as a whole. The week
 * lives in the URL, read and navigated the way every list page in the
 * product reads its search params.
 */
export const MyWeekPage = () => {
  const { t, formatters } = useI18n("time");
  const { week } = useSearch({ strict: false }) as MyWeekSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const queryClient = useQueryClient();
  const dates = useDayFormat();

  const weekStart = mondayOf(isIsoDate(week) ? week : today());
  const days = weekDays(weekStart);
  const goTo = (monday: string | undefined) => navigate({ search: { week: monday } });

  const { data, isPending, isError, error } = useQuery(weekQueryOptions(weekStart));
  const { data: settings } = useQuery(timeSettingsQueryOptions());

  const submit = useMutation({
    mutationFn: () => submitWeek(weekStart),
    onSuccess: () => {
      notifications.show({ color: "teal", title: t("weekSubmitted"), message: t("weekSubmittedMessage") });
      return queryClient.invalidateQueries({ queryKey: ["time"] });
    },
    onError: (failure) =>
      notifications.show({ color: "red", title: t("couldNotSubmitWeek"), message: refusalMessage(failure) }),
  });

  const confirmSubmit = () =>
    modals.openConfirmModal({
      title: t("submitWeekTitle"),
      children: (
        <Text size="sm">{t("submitWeekConfirm", { from: dates.medium(days[0]), to: dates.medium(days[6]) })}</Text>
      ),
      labels: { confirm: t("submit"), cancel: t("cancel") },
      onConfirm: () => submit.mutate(),
    });

  const lockedBefore = settings?.lockedBefore;
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("myWeek")}
        description={t("myWeekDescription")}
        actions={
          <Button onClick={confirmSubmit} loading={submit.isPending} disabled={!data}>
            {t("submitWeek")}
          </Button>
        }
      />

      <Group justify="space-between" wrap="wrap">
        <Group gap="xs" wrap="nowrap">
          <ActionIcon variant="default" aria-label={t("previousWeek")} onClick={() => goTo(addDays(weekStart, -7))}>
            <IconChevronLeft size={16} />
          </ActionIcon>
          <Button variant="default" size="xs" onClick={() => goTo(undefined)}>
            {t("thisWeek")}
          </Button>
          <ActionIcon variant="default" aria-label={t("nextWeek")} onClick={() => goTo(addDays(weekStart, 7))}>
            <IconChevronRight size={16} />
          </ActionIcon>
          <Text fw={600}>{t("weekRange", { from: dates.medium(days[0]), to: dates.medium(days[6]) })}</Text>
        </Group>
        {data?.submittedAt && (
          <Badge variant="light" color="blue">
            {t("submittedOn", {
              date: formatters.formatDate(data.submittedAt, { dateStyle: "medium", timeStyle: "short" }),
            })}
          </Badge>
        )}
      </Group>

      {data?.hasUnsubmittedChanges && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={t("unsubmittedChangesTitle")}>
          {t("unsubmittedChanges")}
        </Alert>
      )}

      {lockedBefore && days[0] < lockedBefore && (
        <Group gap="xs" c="dimmed">
          <IconLock size={16} />
          <Text size="sm">{t("lockedUntil", { date: dates.medium(lockedBefore) })}</Text>
        </Group>
      )}

      <Card withBorder padding="lg" radius="md">
        {isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadWeek")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={5} rowHeight={44} />}
        {data && <WeekGrid key={weekStart} week={data} days={days} settings={settings} />}
      </Card>
    </Stack>
  );
};

interface WeekGridProps {
  week: TimeWeek;
  days: string[];
  settings: TimeSettings | undefined;
}

/**
 * The grid itself, keyed by the week so the rows added on the page — which
 * exist only here until a day in them gets hours — belong to the week they
 * were added in.
 */
const WeekGrid = ({ week, days, settings }: WeekGridProps) => {
  const { t } = useI18n("time");
  const queryClient = useQueryClient();
  const hours = useHoursFormat();
  const [added, setAdded] = useState<WeekRowRef[]>([]);
  const [picking, setPicking] = useState(false);

  const serverKeys = new Set(week.rows.map(rowKey));
  const rows: TimeWeekRow[] = [
    ...week.rows,
    ...added.filter((row) => !serverKeys.has(rowKey(row))).map((row) => ({ ...row, days: [] })),
  ];

  // Kept against the rows added here rather than against the week's own: a
  // row whose last entry was just deleted is still in the response being
  // read back, and it is exactly the row that has to stay on the page. A
  // duplicate of a row the server answers is filtered out above instead.
  const addRows = (refs: WeekRowRef[]) =>
    setAdded((current) => {
      const known = new Set(current.map(rowKey));
      const next = [...current];
      for (const ref of refs) {
        if (known.has(rowKey(ref))) continue;
        known.add(rowKey(ref));
        next.push(ref);
      }
      return next;
    });

  // Rows for the caller's open tasks — only on projects that take time, the
  // same projects the picker offers.
  const fromTasks = useMutation({
    mutationFn: async (): Promise<WeekRowRef[]> => {
      const [projects, tasks] = await Promise.all([
        queryClient.fetchQuery(myProjectsQueryOptions()),
        queryClient.fetchQuery(myOpenTasksQueryOptions()),
      ]);
      const loggable = new Set(projects.filter(isLoggable).map((project) => project.id));
      return tasks
        .filter((task) => loggable.has(task.projectId))
        .map((task) => ({
          projectId: task.projectId,
          projectCode: task.projectCode,
          projectName: task.projectName,
          billingLineId: null,
          billingLineCode: null,
          trackableCode: null,
          taskId: task.id,
          taskTitle: task.title,
        }));
    },
    onSuccess: (refs) => {
      if (refs.length === 0) {
        notifications.show({ title: t("noOpenTasksToAddTitle"), message: t("noOpenTasksToAdd") });
      }
      addRows(refs);
    },
    onError: (failure) => notifications.show({ color: "red", title: t("fromMyTasks"), message: failure.message }),
  });

  return (
    <Stack gap="md">
      <Group gap="xs">
        <Button variant="light" size="xs" leftSection={<IconPlus size={14} />} onClick={() => setPicking(true)}>
          {t("addRow")}
        </Button>
        <Button
          variant="subtle"
          size="xs"
          leftSection={<IconListCheck size={14} />}
          loading={fromTasks.isPending}
          onClick={() => fromTasks.mutate()}
        >
          {t("fromMyTasks")}
        </Button>
      </Group>

      <RowPicker opened={picking} onClose={() => setPicking(false)} onAdd={(row) => addRows([row])} />

      {rows.length === 0 ? (
        <EmptyState title={t("emptyWeek")} description={t("emptyWeekDescription")} />
      ) : (
        <Table.ScrollContainer minWidth={860}>
          <Table verticalSpacing="xs">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("trackable")}</Table.Th>
                {days.map((date) => (
                  <Table.Th key={date} ta="center">
                    <DayLink date={date} />
                  </Table.Th>
                ))}
                <Table.Th ta="right">{t("total")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows.map((row) => (
                <WeekRowView
                  key={rowKey(row)}
                  row={row}
                  days={days}
                  settings={settings}
                  onKeep={() => addRows([rowRef(row)])}
                />
              ))}
            </Table.Tbody>
            <Table.Tfoot>
              <Table.Tr data-testid="week-totals">
                <Table.Td fw={600}>{t("total")}</Table.Td>
                {days.map((date, i) => (
                  <Table.Td key={date} ta="center" fw={600}>
                    {hours.display(week.totals.perDay[i] ?? 0)}
                  </Table.Td>
                ))}
                <Table.Td ta="right" fw={700} aria-label={t("weekTotal")}>
                  {hours.display(week.totals.week)}
                </Table.Td>
              </Table.Tr>
            </Table.Tfoot>
          </Table>
        </Table.ScrollContainer>
      )}
    </Stack>
  );
};

/** A day's column heading, which opens that day's own view. */
const DayLink = ({ date }: { date: string }) => {
  const { t } = useI18n("time");
  const dates = useDayFormat();
  const Link = useShellLink();
  const to = dayUrl(date);
  const label = t("openDay", { day: dates.long(date) });
  return Link ? (
    <Anchor size="sm" fw={600} aria-label={label} renderRoot={(props) => <Link to={to} {...props} />}>
      {dates.short(date)}
    </Anchor>
  ) : (
    <Anchor size="sm" fw={600} aria-label={label} href={to}>
      {dates.short(date)}
    </Anchor>
  );
};

const sum = (entries: TimeEntry[]) => entries.reduce((total, entry) => total + entry.hours, 0);

/**
 * The work types a row's entries were logged as this week, each once, by
 * name. A row is one trackable (project, line, task) — the key the server
 * groups by — so a work type is a fact about its entries, and the row says
 * which ones it holds (work types design D5).
 */
const workTypesOf = (row: TimeWeekRow): string[] =>
  [
    ...new Set(
      row.days.flatMap((day) => day.entries.flatMap((entry) => (entry.workType ? [entry.workType.name] : []))),
    ),
  ].sort((a, b) => a.localeCompare(b));

interface WeekRowViewProps {
  row: TimeWeekRow;
  days: string[];
  settings?: TimeSettings;
  /** The row's last entry has just gone; keep the row on the page so it can be typed into again. */
  onKeep: () => void;
}

const WeekRowView = ({ row, days, settings, onKeep }: WeekRowViewProps) => {
  const { t } = useI18n("time");
  const dates = useDayFormat();
  const hours = useHoursFormat();
  const label = rowLabel(row);
  const entriesOn = (date: string) => row.days.find((day) => day.date === date)?.entries ?? [];
  return (
    <Table.Tr>
      <Table.Td>
        <Stack gap={0}>
          <Group gap={6} wrap="nowrap">
            <Text size="sm" fw={600}>
              {label}
            </Text>
            {workTypesOf(row).map((name) => (
              <WorkTypeBadge key={name} name={name} />
            ))}
          </Group>
          <Text size="xs" c="dimmed">
            {row.projectName}
          </Text>
        </Stack>
      </Table.Td>
      {days.map((date) => (
        <Table.Td key={date} ta="center">
          <WeekCell
            row={row}
            date={date}
            entries={entriesOn(date)}
            locked={isLocked(date, settings)}
            label={t("hoursCellLabel", { row: label, day: dates.long(date) })}
            onKeep={onKeep}
          />
        </Table.Td>
      ))}
      <Table.Td ta="right" aria-label={t("rowTotal", { row: label })}>
        {hours.display(row.days.reduce((total, day) => total + sum(day.entries), 0))}
      </Table.Td>
    </Table.Tr>
  );
};

interface WeekCellProps {
  row: WeekRowRef;
  date: string;
  entries: TimeEntry[];
  locked: boolean;
  label: string;
  onKeep: () => void;
}

/**
 * One row's day: at most one entry without clock times, which the cell
 * creates, replaces or deletes. A day holding several entries of the row, or
 * one entry worked between a start and an end time, shows the sum read-only
 * and opens the day view instead — the grid writes a duration, and a duration
 * typed over clock times would silently throw them away.
 */
const WeekCell = ({ row, date, entries, locked, label, onKeep }: WeekCellProps) => {
  const { t } = useI18n("time");
  const navigate = useNavigate() as (options: unknown) => void;
  const queryClient = useQueryClient();
  // Bumped on a refused write so the cell remounts showing the saved value.
  const [attempt, setAttempt] = useState(0);
  const several = entries.length > 1;
  const entry = entries.length === 1 ? entries[0] : undefined;

  const save = useMutation({
    mutationFn: async (hours: number | null) => {
      if (entry && hours === null) return deleteTimeEntry(entry.id);
      if (hours === null) return;
      if (entry) return updateTimeEntry(entry.id, timeEntryUpdateFrom(entry, { hours }));
      await createTimeEntry({
        projectId: row.projectId,
        entryDate: date,
        hours,
        ...(row.billingLineId != null && { billingLineId: row.billingLineId }),
        ...(row.taskId != null && { taskId: row.taskId }),
      });
    },
    onSuccess: (_result, hours) => {
      // The server drops a row once its last entry is gone; the person who
      // just cleared it is most likely about to type the day again.
      if (hours === null) onKeep();
      return queryClient.invalidateQueries({ queryKey: ["time"] });
    },
    onError: (failure) => {
      setAttempt((n) => n + 1);
      notifications.show({ color: "red", title: t("couldNotSaveHours"), message: refusalMessage(failure) });
    },
  });

  const timed = entry !== undefined && Boolean(entry.startTime && entry.endTime);
  const inDayView = several || timed;
  const readOnly = locked || inDayView || (entry !== undefined && !entry.capabilities.canEdit);
  let tooltip: string | undefined;
  if (locked) tooltip = t("lockedDay");
  else if (several) tooltip = t("severalEntries", { count: entries.length });
  else if (timed && entry)
    tooltip = t("timedEntry", { range: t("timeRange", { start: entry.startTime, end: entry.endTime }) });
  else if (entry?.status === "rejected")
    tooltip = entry.rejectionReason
      ? t("rejectedBecause", { reason: entry.rejectionReason })
      : t(timeEntryStatusLabelKey(entry.status));
  else if (entry && readOnly) tooltip = t(timeEntryStatusLabelKey(entry.status));

  return (
    <HoursCell
      key={attempt}
      label={label}
      hours={entries.length > 0 ? sum(entries) : undefined}
      status={several ? undefined : entry?.status}
      readOnly={readOnly || save.isPending}
      tooltip={tooltip}
      onCommit={(hours) => save.mutate(hours)}
      onInvalid={() => notifications.show({ color: "red", title: t("invalidHoursTitle"), message: t("invalidHours") })}
      onActivate={inDayView && !locked ? () => navigate({ to: "/time/day", search: { date } }) : undefined}
    />
  );
};
