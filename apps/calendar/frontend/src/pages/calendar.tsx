import "@fullcalendar/react/skeleton.css";
import "@fullcalendar/react/themes/breezy/theme.css";
import "../styles/fullcalendar.css";
import FullCalendar, {
  type DateSelectInfo,
  type DatesSetInfo,
  type DidMountHandler,
  type EventApi,
  type EventClickInfo,
  type EventDisplayInfo,
  type EventDropInfo,
  type EventResizeDoneInfo,
  useCalendarController,
} from "@fullcalendar/react";
import dayGridPlugin from "@fullcalendar/react/daygrid";
import interactionPlugin from "@fullcalendar/react/interaction";
import listPlugin from "@fullcalendar/react/list";
import nbLocale from "@fullcalendar/react/locales/nb";
import breezyTheme from "@fullcalendar/react/themes/breezy";
import timeGridPlugin from "@fullcalendar/react/timegrid";
import { ActionIcon, Alert, Button, em, Group, Paper, SegmentedControl, Stack, Title } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconChevronLeft, IconChevronRight, IconPlus } from "@tabler/icons-react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearch } from "@tanstack/react-router";
import { instantFromWallClock, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useEffect, useMemo, useState } from "react";
import { appointmentQueryOptions, appointmentWriteOf, putOccurrence, updateAppointment } from "../api/appointments";
import { type CalendarItem, calendarItemsQueryOptions, colleagueItemsQueryOptions } from "../api/items";
import { calendarMetaQueryOptions } from "../api/meta";
import { CALENDAR_QUERY_KEY } from "../api/request";
import { AppointmentSheet } from "../components/appointment-sheet";
import { ColleaguePicker } from "../components/colleague-picker";
import { type OccurrenceAnswer, OccurrenceQuestion } from "../components/occurrence-question";
import { SourceLegend } from "../components/source-legend";
import "../i18n";
import { APPOINTMENT_KIND, HOLIDAYS_KIND } from "../lib/colours";
import { addDays, daysBetween } from "../lib/dates";
import { useHiddenKinds } from "../lib/filter";
import { type CalendarEventProps, holidayEventInput, toEventInput } from "../lib/items";
import { useCalendarNavigate } from "../lib/navigate";
import { type CalendarSearch, type CalendarView, DEFAULT_CALENDAR_VIEW } from "../lib/search";
import { type Span, shiftSeries, spanFields, spanOf } from "../lib/span";

export type { CalendarView } from "../lib/search";

const fullCalendarViews: Record<CalendarView, string> = {
  month: "dayGridMonth",
  week: "timeGridWeek",
  day: "timeGridDay",
  list: "listWeek",
};

// Module-level, not inline: FullCalendar compares options by reference, and a
// new array or object on every render re-applies them, fires `datesSet`,
// which the controller turns into another render — an endless loop.
const plugins = [breezyTheme, dayGridPlugin, timeGridPlugin, listPlugin, interactionPlugin];
const locales = [nbLocale];
/** "09:00", not "9a", in English as in Norwegian. */
const timeFormat = { hour: "2-digit", minute: "2-digit", hour12: false } as const;

/**
 * The tooltip of a drawn item: its title, with its one line after it — the
 * customer, the project, a subscription's name (feed design D9).
 */
const titleTooltip: DidMountHandler<EventDisplayInfo> = ({ el, event }) => {
  const { item } = event.extendedProps as CalendarEventProps;
  el.title = item?.subtitle ? `${event.title} · ${item.subtitle}` : event.title;
};

/** The longest window `/items` answers (design D5). */
const MAX_WINDOW_DAYS = 93;

/**
 * The instant a FullCalendar date string means. With a named zone it may
 * carry its offset or be the wall-clock time in the zone; both read right.
 */
const instantOf = (value: string, timeZone: string): string =>
  /(Z|[+-]\d{2}:?\d{2})$/.test(value)
    ? new Date(value).toISOString()
    : instantFromWallClock(value.slice(0, 10), value.slice(11, 16) || "00:00", timeZone).toISOString();

/** A selection's or an event's span: FullCalendar's all-day end is exclusive, the API's last day is not. */
const spanOfRange = (
  range: { allDay: boolean; startStr: string; endStr: string | null | undefined },
  timeZone: string,
): Span => {
  if (range.allDay) {
    const startDate = range.startStr.slice(0, 10);
    const endDate = range.endStr ? addDays(range.endStr.slice(0, 10), -1) : startDate;
    return { allDay: true, startDate, endDate: endDate < startDate ? startDate : endDate };
  }
  const startsAt = instantOf(range.startStr, timeZone);
  const endsAt = range.endStr
    ? instantOf(range.endStr, timeZone)
    : new Date(Date.parse(startsAt) + 60 * 60 * 1000).toISOString();
  return { allDay: false, startsAt, endsAt };
};

const spanOfEvent = (event: EventApi, timeZone: string): Span =>
  spanOfRange({ allDay: event.allDay, startStr: event.startStr, endStr: event.endStr }, timeZone);

interface PendingMove {
  item: CalendarItem;
  from: Span;
  to: Span;
  revert: () => void;
}

type MoveRequest = PendingMove & { kind: "single" | OccurrenceAnswer };

/** The sheet the page opened itself, carrying what the URL cannot: a selection, an occurrence as drawn. */
interface LocalSheet {
  span?: Span;
  occurrenceSpan?: Span;
}

export function CalendarPage() {
  const { t, locale, timeZone } = useI18n("calendar");
  const search = useSearch({ strict: false }) as CalendarSearch;
  const navigate = useCalendarNavigate();
  const queryClient = useQueryClient();
  const controller = useCalendarController();
  // Read at once, not after a first render: the view a phone opens on is decided on mount.
  const phone = useMediaQuery(`(max-width: ${em(767.98)})`, false, { getInitialValueInEffect: false });
  const view: CalendarView = search.view ?? (phone ? "list" : DEFAULT_CALENDAR_VIEW);
  const meta = useQuery(calendarMetaQueryOptions());
  const [hidden, toggleKind] = useHiddenKinds();
  const [range, setRange] = useState<{ from: string; to: string } | null>(null);
  const [localSheet, setLocalSheet] = useState<LocalSheet | null>(null);
  const [pendingMove, setPendingMove] = useState<PendingMove | null>(null);

  // A colleague's calendar only for whoever may see it; the URL alone grants nothing.
  const colleague = search.user && meta.data?.canViewColleagues ? search.user : undefined;
  const window = { from: range?.from ?? "", to: range?.to ?? "", timeZone };
  const own = useQuery({
    ...calendarItemsQueryOptions(window),
    enabled: range !== null && !colleague && !(search.user && meta.isPending),
    placeholderData: keepPreviousData,
  });
  const theirs = useQuery({
    ...colleagueItemsQueryOptions(colleague ?? "", window),
    enabled: range !== null && colleague !== undefined,
    placeholderData: keepPreviousData,
  });
  const items = colleague ? theirs : own;

  useEffect(() => {
    const type = fullCalendarViews[view];
    if (controller.view && controller.view.type !== type) controller.changeView(type);
  }, [controller, view]);

  const busyLabel = t("busy");
  const events = useMemo(() => {
    if (!items.data) return [];
    const drawn = items.data.items
      .filter((item) => !hidden.has(item.kind))
      .map((item) => toEventInput(item, { busyLabel, readOnly: colleague !== undefined }));
    return hidden.has(HOLIDAYS_KIND) ? drawn : [...drawn, ...items.data.publicHolidays.map(holidayEventInput)];
  }, [items.data, hidden, busyLabel, colleague]);

  const kinds = useMemo(() => {
    const sources = colleague ? [] : (meta.data?.sources ?? items.data?.sources ?? []).map((source) => source.kind);
    return [...new Set([APPOINTMENT_KIND, ...sources, HOLIDAYS_KIND])];
  }, [colleague, meta.data, items.data]);

  const changeView = (next: string) =>
    void navigate({ search: (previous) => ({ ...previous, view: next as CalendarView }), replace: true });

  const onDatesSet = (info: DatesSetInfo) => {
    const from = info.startStr.slice(0, 10);
    const lastDay = addDays(info.endStr.slice(0, 10), -1);
    const to = daysBetween(from, lastDay) >= MAX_WINDOW_DAYS ? addDays(from, MAX_WINDOW_DAYS - 1) : lastDay;
    setRange((current) => (current?.from === from && current.to === to ? current : { from, to }));
    // The middle of the window is always inside the period shown, whatever the grid pads it with.
    const date = addDays(from, Math.floor(daysBetween(from, to) / 2));
    if (search.date !== date) void navigate({ search: (previous) => ({ ...previous, date }), replace: true });
  };

  const openAppointment = (item: CalendarItem) => {
    if (!item.appointment) return;
    setLocalSheet({ occurrenceSpan: item.appointment.occurrenceDate ? spanOf(item) : undefined });
    void navigate({
      search: (previous) => ({
        ...previous,
        appointment: item.appointment?.id,
        occurrence: item.appointment?.occurrenceDate,
      }),
    });
  };

  const onEventClick = (info: EventClickInfo) => {
    const { item } = info.event.extendedProps as CalendarEventProps;
    if (!item) return;
    info.jsEvent.preventDefault();
    if (item.appointment) {
      // A colleague's appointment is theirs; the sheet would answer 404.
      if (!colleague) openAppointment(item);
      return;
    }
    // A subscription's item has no path: it is the other calendar's, and opens nothing.
    if (item.path) void navigate({ href: item.path });
  };

  const onSelect = (info: DateSelectInfo) => {
    info.view.calendar.unselect();
    if (colleague) return;
    setLocalSheet({ span: spanOfRange(info, timeZone) });
  };

  const closeSheet = () => {
    setLocalSheet(null);
    if (search.appointment !== undefined || search.new || search.customerId || search.projectId)
      void navigate({
        search: (previous) => ({
          ...previous,
          appointment: undefined,
          occurrence: undefined,
          new: undefined,
          customerId: undefined,
          projectId: undefined,
        }),
        replace: true,
      });
  };

  const move = useMutation({
    mutationFn: async ({ item, from, to, kind }: MoveRequest) => {
      const appointment = item.appointment;
      if (!appointment) throw new Error("not an appointment");
      if (kind === "occurrence") return putOccurrence(appointment.id, appointment.occurrenceDate ?? "", spanFields(to));
      const current = await queryClient.fetchQuery({ ...appointmentQueryOptions(appointment.id), staleTime: 0 });
      const next = kind === "series" ? shiftSeries(spanOf(current), from, to) : to;
      return updateAppointment(current.id, current.revision, appointmentWriteOf(current, next));
    },
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("appointmentMoved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [CALENDAR_QUERY_KEY] });
    },
    onError: async (error: Error, { revert }) => {
      revert();
      notifications.show({ color: "red", title: t("couldNotMove"), message: error.message });
      await queryClient.invalidateQueries({ queryKey: [CALENDAR_QUERY_KEY] });
    },
  });

  const onEventChange = (info: EventDropInfo | EventResizeDoneInfo) => {
    const { item } = info.event.extendedProps as CalendarEventProps;
    if (!item?.appointment?.isOwner || colleague) return info.revert();
    const change = { item, from: spanOf(item), to: spanOfEvent(info.event, timeZone), revert: info.revert };
    if (item.appointment.recurring && item.appointment.occurrenceDate) setPendingMove(change);
    else move.mutate({ ...change, kind: "single" });
  };

  const answerMove = (answer: OccurrenceAnswer) => {
    if (pendingMove) move.mutate({ ...pendingMove, kind: answer });
    setPendingMove(null);
  };

  const cancelMove = () => {
    pendingMove?.revert();
    setPendingMove(null);
  };

  const sheetOpen = search.appointment !== undefined || search.new === 1 || localSheet !== null;
  const buttons = controller.getButtonState();

  return (
    <Stack gap="md">
      <PageHeader
        title={t("calendar")}
        description={t("calendarDescription")}
        actions={
          !colleague && (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setLocalSheet({})}>
              {t("newAppointment")}
            </Button>
          )
        }
      />
      <Group justify="space-between" wrap="wrap" gap="sm">
        <Group gap="xs" wrap="nowrap">
          <Button variant="default" onClick={() => controller.today()} disabled={buttons.today.isDisabled}>
            {t("today")}
          </Button>
          <ActionIcon variant="default" size="lg" aria-label={t("previous")} onClick={() => controller.prev()}>
            <IconChevronLeft size={18} />
          </ActionIcon>
          <ActionIcon variant="default" size="lg" aria-label={t("next")} onClick={() => controller.next()}>
            <IconChevronRight size={18} />
          </ActionIcon>
          <Title order={2} size="h4" fw={600} data-testid="calendar-title">
            {controller.view?.title}
          </Title>
        </Group>
        <Group gap="sm" wrap="wrap">
          {meta.data?.canViewColleagues && (
            <ColleaguePicker
              value={search.user ?? null}
              onChange={(user) =>
                void navigate({ search: (previous) => ({ ...previous, user: user ?? undefined }), replace: true })
              }
            />
          )}
          <SegmentedControl
            aria-label={t("view")}
            value={view}
            onChange={changeView}
            data={[
              { value: "month", label: t("viewMonth") },
              { value: "week", label: t("viewWeek") },
              { value: "day", label: t("viewDay") },
              { value: "list", label: t("viewList") },
            ]}
          />
        </Group>
      </Group>
      <SourceLegend kinds={kinds} hidden={hidden} onToggle={toggleKind} />
      {items.isError && (
        <Alert color="red" title={t("couldNotLoad")}>
          {items.error.message}
        </Alert>
      )}
      <Paper withBorder radius="md" p={0} className="vantigo-calendar" style={{ overflow: "hidden" }}>
        <FullCalendar
          controller={controller}
          plugins={plugins}
          initialView={fullCalendarViews[view]}
          initialDate={search.date}
          headerToolbar={false}
          locales={locales}
          locale={locale}
          timeZone={timeZone}
          firstDay={1}
          // The time grids scroll inside a fixed height, opening on the
          // working day; month and list grow with their content.
          height={view === "week" || view === "day" ? 720 : "auto"}
          scrollTime="07:00:00"
          eventTimeFormat={timeFormat}
          slotHeaderFormat={timeFormat}
          nowIndicator
          selectable={!colleague}
          selectMirror
          dayMaxEvents
          events={events}
          datesSet={onDatesSet}
          select={onSelect}
          eventClick={onEventClick}
          eventDidMount={titleTooltip}
          eventDrop={onEventChange}
          eventResize={onEventChange}
        />
      </Paper>
      <AppointmentSheet
        opened={sheetOpen}
        onClose={closeSheet}
        appointmentId={search.appointment}
        occurrenceDate={search.occurrence}
        occurrenceSpan={localSheet?.occurrenceSpan}
        span={localSheet?.span}
        customerId={search.customerId}
        projectId={search.projectId}
      />
      <OccurrenceQuestion opened={pendingMove !== null} action="save" onAnswer={answerMove} onCancel={cancelMove} />
    </Stack>
  );
}
