import "@fullcalendar/react/skeleton.css";
import "@fullcalendar/react/themes/breezy/theme.css";
import "../styles/fullcalendar.css";
import FullCalendar, {
  type DateSelectInfo,
  type EventClickInfo,
  type EventInput,
  useCalendarController,
} from "@fullcalendar/react";
import dayGridPlugin from "@fullcalendar/react/daygrid";
import interactionPlugin from "@fullcalendar/react/interaction";
import listPlugin from "@fullcalendar/react/list";
import nbLocale from "@fullcalendar/react/locales/nb";
import breezyTheme from "@fullcalendar/react/themes/breezy";
import timeGridPlugin from "@fullcalendar/react/timegrid";
import { ActionIcon, Button, Group, Paper, SegmentedControl, Stack, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconChevronLeft, IconChevronRight } from "@tabler/icons-react";
import { civilDateIn, instantFromWallClock, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useMemo, useState } from "react";

export type CalendarView = "month" | "week" | "day" | "list";

const fullCalendarViews: Record<CalendarView, string> = {
  month: "dayGridMonth",
  week: "timeGridWeek",
  day: "timeGridDay",
  list: "listWeek",
};

// Module-level, not inline: FullCalendar compares options by reference, and a
// new array on every render re-applies them, fires `datesSet`, which the
// controller turns into another render — an endless loop.
const plugins = [breezyTheme, dayGridPlugin, timeGridPlugin, listPlugin, interactionPlugin];
const locales = [nbLocale];

/** The `YYYY-MM-DD` `days` after `date`, on the calendar alone. */
const addDays = (date: string, days: number): string => {
  const [y, m, d] = date.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d + days)).toISOString().slice(0, 10);
};

/**
 * The spike's stand-in for `GET /calendar/items`: three timed appointments as
 * the API will send them (instants), two all-day items and one spanning three
 * days (civil dates, end exclusive), all around today in the viewer's zone.
 */
const fakeEvents = (zone: string): EventInput[] => {
  const today = civilDateIn(zone);
  const at = (offset: number, time: string) => instantFromWallClock(addDays(today, offset), time, zone).toISOString();
  return [
    { id: "a1", title: "Kickoff with Nordlys AS", start: at(0, "09:00"), end: at(0, "10:30") },
    { id: "a2", title: "One-to-one with Kari", start: at(1, "13:00"), end: at(1, "13:30") },
    {
      id: "a3",
      title: "Site visit, Bergen",
      start: at(2, "08:15"),
      end: at(2, "11:45"),
      color: "var(--vantigo-calendar-teal)",
    },
    { id: "d1", title: "Invoice run", start: today, allDay: true },
    {
      id: "d2",
      title: "Ola on holiday",
      start: addDays(today, 3),
      allDay: true,
      color: "var(--vantigo-calendar-orange)",
    },
    { id: "d3", title: "Trade fair, Lillestrøm", start: addDays(today, 4), end: addDays(today, 7), allDay: true },
  ];
};

export function CalendarPage() {
  const { t, locale, timeZone } = useI18n("calendar");
  const controller = useCalendarController();
  const [view, setView] = useState<CalendarView>("month");
  const events = useMemo(() => fakeEvents(timeZone), [timeZone]);

  const changeView = (next: string) => {
    const nextView = next as CalendarView;
    setView(nextView);
    controller.changeView(fullCalendarViews[nextView]);
  };

  const onSelect = (info: DateSelectInfo) => {
    notifications.show({
      title: t("newAppointment"),
      message: t("newAppointmentComingSoon", { range: `${info.startStr} – ${info.endStr}` }),
    });
    info.view.calendar.unselect();
  };

  const onEventClick = (info: EventClickInfo) => {
    notifications.show({ title: t("appointment"), message: t("appointmentComingSoon", { title: info.event.title }) });
  };

  const buttons = controller.getButtonState();

  return (
    <Stack gap="md">
      <PageHeader title={t("calendar")} description={t("calendarDescription")} />
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
      <Paper withBorder radius="md" p={0} className="vantigo-calendar" style={{ overflow: "hidden" }}>
        <FullCalendar
          controller={controller}
          plugins={plugins}
          initialView={fullCalendarViews.month}
          headerToolbar={false}
          locales={locales}
          locale={locale}
          timeZone={timeZone}
          firstDay={1}
          // The time grids scroll inside a fixed height, opening on the
          // working day; month and list grow with their content.
          height={view === "week" || view === "day" ? 720 : "auto"}
          scrollTime="07:00:00"
          nowIndicator
          selectable
          selectMirror
          dayMaxEvents
          events={events}
          select={onSelect}
          eventClick={onEventClick}
        />
      </Paper>
    </Stack>
  );
}
