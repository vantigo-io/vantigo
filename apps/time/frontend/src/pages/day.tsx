import { ActionIcon, Alert, Anchor, Button, Card, Group, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconArrowBackUp,
  IconCalendarWeek,
  IconChevronLeft,
  IconChevronRight,
  IconLock,
  IconPencil,
  IconPlus,
  IconTrash,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n, useShellLink } from "@vantigo/frontend-shell";
import { useState } from "react";
import { unapproveTimeEntries } from "../api/approvals";
import { deleteTimeEntry, type TimeEntry } from "../api/entries";
import { isLocked, timeSettingsQueryOptions } from "../api/settings";
import { weekQueryOptions } from "../api/weeks";
import { EntryStatusBadge } from "../components/entry-status-badge";
import { RateLine } from "../components/rate-line";
import { RefusalList } from "../components/refusal-list";
import { WorkTypeBadge } from "../components/work-type-badge";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useHoursFormat } from "../lib/hours";
import { rowLabel } from "../lib/rows";
import { addDays, isIsoDate, mondayOf, today, weekUrl } from "../lib/week";
import { EntryFormModal, type EntryModalState } from "./-entry-form-modal";

/** The page's URL search params, which the host route validates (Task 7). An absent date is today. */
export interface DaySearch {
  date?: string;
}

/** Clock-timed entries first, in the order they were worked; then the rest as they were logged. */
const byTimeOfDay = (a: TimeEntry, b: TimeEntry): number => {
  if (a.startTime && b.startTime) return a.startTime.localeCompare(b.startTime) || a.id - b.id;
  if (a.startTime) return -1;
  if (b.startTime) return 1;
  return a.id - b.id;
};

/**
 * The day view (design §8): one day of the caller's own time as a list,
 * mobile first, with each entry's start and end time and note — the place
 * to log time by the clock, and to change a day the week grid shows as a
 * sum. The day is read from the caller's week, the same response the grid
 * reads, so the two never disagree.
 */
export const DayPage = () => {
  const { t, formatters } = useI18n("time");
  const { date: requested } = useSearch({ strict: false }) as DaySearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const Link = useShellLink();
  const hours = useHoursFormat();

  const date = isIsoDate(requested) ? requested : today();
  const weekStart = mondayOf(date);
  const goTo = (next: string | undefined) => navigate({ search: { date: next } });

  const { data, isPending, isError, error } = useQuery(weekQueryOptions(weekStart));
  const { data: settings } = useQuery(timeSettingsQueryOptions());
  const locked = isLocked(date, settings);
  const [modal, setModal] = useState<EntryModalState | null>(null);

  const entries = (data?.rows ?? [])
    .flatMap((row) => row.days.find((day) => day.date === date)?.entries ?? [])
    .sort(byTimeOfDay);
  const total = entries.reduce((sum, entry) => sum + entry.hours, 0);

  const weekHref = weekUrl(weekStart);
  const weekLink = Link ? (
    <Anchor size="sm" renderRoot={(props) => <Link to={weekHref} {...props} />}>
      <Group gap={4} component="span">
        <IconCalendarWeek size={16} />
        {t("backToWeek")}
      </Group>
    </Anchor>
  ) : (
    <Anchor size="sm" href={weekHref}>
      <Group gap={4} component="span">
        <IconCalendarWeek size={16} />
        {t("backToWeek")}
      </Group>
    </Anchor>
  );

  return (
    <Stack gap="lg">
      <PageHeader
        title={formatters.formatDate(date, { weekday: "long", day: "numeric", month: "long", timeZone: "UTC" })}
        description={t("dayDescription")}
        actions={
          !locked && (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setModal({ mode: "create", date })}>
              {t("addEntry")}
            </Button>
          )
        }
      />

      <EntryFormModal state={modal} onClose={() => setModal(null)} />

      <Group justify="space-between" wrap="wrap">
        <Group gap="xs" wrap="nowrap">
          <ActionIcon variant="default" aria-label={t("previousDay")} onClick={() => goTo(addDays(date, -1))}>
            <IconChevronLeft size={16} />
          </ActionIcon>
          <Button variant="default" size="xs" onClick={() => goTo(undefined)}>
            {t("today")}
          </Button>
          <ActionIcon variant="default" aria-label={t("nextDay")} onClick={() => goTo(addDays(date, 1))}>
            <IconChevronRight size={16} />
          </ActionIcon>
        </Group>
        {weekLink}
      </Group>

      {locked && (
        <Group gap="xs" c="dimmed">
          <IconLock size={16} />
          <Text size="sm">{t("lockedDayNotice")}</Text>
        </Group>
      )}

      {isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadDay")}>
          {error.message}
        </Alert>
      )}
      {isPending && <ContentSkeleton rows={3} rowHeight={72} />}

      {data && entries.length === 0 && (
        <Card withBorder padding="lg" radius="md">
          <EmptyState title={t("noEntries")} description={t("noEntriesDescription")} />
        </Card>
      )}

      {entries.length > 0 && (
        <Stack gap="sm">
          {entries.map((entry) => (
            <EntryCard
              key={entry.id}
              entry={entry}
              editable={!locked && entry.capabilities.canEdit}
              onEdit={() => setModal({ mode: "edit", entry })}
            />
          ))}
          <Group justify="space-between" px="md">
            <Text fw={600}>{t("dayTotal")}</Text>
            <Text fw={700} data-testid="day-total">
              {t("hoursShort", { hours: hours.display(total) })}
            </Text>
          </Group>
        </Stack>
      )}
    </Stack>
  );
};

interface EntryCardProps {
  entry: TimeEntry;
  editable: boolean;
  onEdit: () => void;
}

const EntryCard = ({ entry, editable, onEdit }: EntryCardProps) => {
  const { t } = useI18n("time");
  const queryClient = useQueryClient();
  const hours = useHoursFormat();
  const label = rowLabel(entry);
  const duration = hours.display(entry.hours);

  const remove = useMutation({
    mutationFn: () => deleteTimeEntry(entry.id),
    onSuccess: () => {
      notifications.show({ color: "teal", title: t("entryDeleted"), message: label });
      return queryClient.invalidateQueries({ queryKey: ["time"] });
    },
    onError: (failure) =>
      notifications.show({ color: "red", title: t("couldNotDeleteEntry"), message: refusalMessage(failure) }),
  });

  // The approval queue holds submitted entries only, so an approved one is
  // taken back here — the one place its approver still sees it.
  const unapprove = useMutation({
    mutationFn: () => unapproveTimeEntries([entry.id]),
    onSuccess: () => {
      notifications.show({ color: "teal", title: t("entriesUnapproved"), message: label });
      return queryClient.invalidateQueries({ queryKey: ["time"] });
    },
    onError: (failure) =>
      notifications.show({ color: "red", title: t("couldNotUnapprove"), message: <RefusalList error={failure} /> }),
  });

  const confirmRemove = () =>
    modals.openConfirmModal({
      title: t("deleteEntryTitle"),
      children: <Text size="sm">{t("deleteEntryConfirm", { hours: duration, trackable: label })}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });

  return (
    <Card withBorder padding="md" radius="md" data-entry={entry.id}>
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Stack gap={2} miw={0}>
          <Group gap={6} wrap="nowrap">
            <Text fw={600} size="sm">
              {label}
            </Text>
            {entry.workType && <WorkTypeBadge name={entry.workType.name} />}
          </Group>
          <Text size="xs" c="dimmed">
            {entry.projectName}
          </Text>
          <RateLine billing={entry.billing} />
          {entry.startTime && entry.endTime && (
            <Text size="sm">{t("timeRange", { start: entry.startTime, end: entry.endTime })}</Text>
          )}
          {entry.note && (
            <Text size="sm" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
              {entry.note}
            </Text>
          )}
          {entry.status === "rejected" && entry.rejectionReason && (
            <Text size="sm" c="red">
              {t("rejectedBecause", { reason: entry.rejectionReason })}
            </Text>
          )}
        </Stack>
        <Stack gap={6} align="flex-end">
          <Text fw={700}>{t("hoursShort", { hours: duration })}</Text>
          <EntryStatusBadge status={entry.status} size="sm" />
          {entry.capabilities.canUnapprove && (
            <ActionIcon
              variant="subtle"
              aria-label={t("unapprove")}
              loading={unapprove.isPending}
              onClick={() => unapprove.mutate()}
            >
              <IconArrowBackUp size={16} />
            </ActionIcon>
          )}
          {editable && (
            <Group gap={4} wrap="nowrap">
              <ActionIcon variant="subtle" aria-label={t("editEntry")} onClick={onEdit}>
                <IconPencil size={16} />
              </ActionIcon>
              <ActionIcon
                variant="subtle"
                color="red"
                aria-label={t("deleteEntry")}
                loading={remove.isPending}
                onClick={confirmRemove}
              >
                <IconTrash size={16} />
              </ActionIcon>
            </Group>
          )}
        </Stack>
      </Group>
    </Card>
  );
};
