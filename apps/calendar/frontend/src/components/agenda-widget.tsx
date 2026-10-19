import { Anchor, Button, Group, Stack, Text, UnstyledButton } from "@mantine/core";
import { IconChevronDown, IconChevronRight } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { civilDateIn, useI18n, WidgetCard } from "@vantigo/frontend-shell";
import { type JSX, useState } from "react";
import { type CalendarItem, calendarItemsQueryOptions } from "../api/items";
import "../i18n";
import { addDays, civilDateValue } from "../lib/dates";
import { useCalendarNavigate } from "../lib/navigate";
import { CALENDAR_ROUTE_PATH } from "../lib/routes";
import { ItemLink } from "./item-link";

const AGENDA_DAYS = 7;

/** The day of the window an item belongs to: an all-day one on each day it spans, a timed one on the day it starts. */
const coversDay = (item: CalendarItem, day: string, timeZone: string, firstDay: string): boolean => {
  if (item.allDay) return (item.startDate ?? "") <= day && day <= (item.endDate ?? item.startDate ?? "");
  const starts = civilDateIn(timeZone, new Date(item.startsAt ?? ""));
  // One that began before the window — a trip under way — shows on its first day.
  return day === firstDay ? starts <= day : starts === day;
};

/** All-day first, then by the instant they start: the order `/items` answers in, kept per day. */
const byStart = (a: CalendarItem, b: CalendarItem) =>
  a.allDay !== b.allDay ? (a.allDay ? -1 : 1) : (a.startsAt ?? "").localeCompare(b.startsAt ?? "");

/** Where an item leads: a provider's record, or the appointment on the calendar with its sheet open. */
const hrefOf = (item: CalendarItem, day: string): string => {
  if (item.path) return item.path;
  const params = new URLSearchParams({ date: day });
  if (item.appointment) {
    params.set("appointment", String(item.appointment.id));
    if (item.appointment.occurrenceDate) params.set("occurrence", item.appointment.occurrenceDate);
  }
  return `${CALENDAR_ROUTE_PATH}?${params}`;
};

/**
 * The dashboard's "Today and this week" (design D7): today's items in order
 * with their times, each a link to its record or its appointment (a
 * subscription's item to nothing), and the next six days collapsed to a line
 * each that opens on a click.
 */
export function AgendaWidget(): JSX.Element {
  const { t, timeZone, formatters } = useI18n("calendar");
  const navigate = useCalendarNavigate();
  const today = civilDateIn(timeZone);
  const items = useQuery(calendarItemsQueryOptions({ from: today, to: addDays(today, AGENDA_DAYS - 1), timeZone }));
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());

  const days = Array.from({ length: AGENDA_DAYS }, (_, offset) => addDays(today, offset));
  const itemsOn = (day: string) =>
    (items.data?.items ?? []).filter((item) => coversDay(item, day, timeZone, today)).sort(byStart);
  const holidaysOn = (day: string) => (items.data?.publicHolidays ?? []).filter((holiday) => holiday.date === day);
  const time = (instant: string) =>
    formatters.formatDate(instant, { hour: "2-digit", minute: "2-digit", hour12: false });
  const dayLabel = (day: string) =>
    formatters.formatDate(civilDateValue(day), { weekday: "short", day: "numeric", month: "short", timeZone: "UTC" });
  const toggle = (day: string) =>
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(day)) next.delete(day);
      else next.add(day);
      return next;
    });

  const newAppointment = (
    <Button size="xs" variant="light" onClick={() => void navigate({ to: CALENDAR_ROUTE_PATH, search: { new: 1 } })}>
      {t("newAppointment")}
    </Button>
  );
  const openCalendar = (
    <Anchor
      size="sm"
      href={CALENDAR_ROUTE_PATH}
      onClick={(event) => {
        event.preventDefault();
        void navigate({ to: CALENDAR_ROUTE_PATH });
      }}
    >
      {t("agenda.openCalendar")}
    </Anchor>
  );

  const list = (day: string) => {
    const dayItems = itemsOn(day);
    return (
      <Stack gap={4}>
        {holidaysOn(day).map((holiday) => (
          <Text key={holiday.date} size="sm" c="dimmed">
            {holiday.name}
          </Text>
        ))}
        {dayItems.map((item) => (
          <Group key={`${item.kind}:${item.id}`} gap="sm" wrap="nowrap" align="baseline">
            <Text size="sm" c="dimmed" w={96} style={{ flexShrink: 0 }}>
              {item.allDay
                ? t("allDay")
                : item.endsAt && item.endsAt !== item.startsAt
                  ? t("timeRange", { start: time(item.startsAt ?? ""), end: time(item.endsAt) })
                  : time(item.startsAt ?? "")}
            </Text>
            <Stack gap={0} style={{ minWidth: 0 }}>
              <Text size="sm" td={item.done || item.appointment?.myStatus === "declined" ? "line-through" : undefined}>
                {/* A subscription's item is the other calendar's and leads nowhere (feed design D9). */}
                {item.subscription ? (
                  item.title || t("busy")
                ) : (
                  <ItemLink href={hrefOf(item, day)}>{item.title || t("busy")}</ItemLink>
                )}
              </Text>
              {item.subtitle && (
                <Text size="xs" c="dimmed" truncate>
                  {item.subtitle}
                </Text>
              )}
            </Stack>
          </Group>
        ))}
      </Stack>
    );
  };

  const empty = items.data !== undefined && items.data.items.length === 0 && items.data.publicHolidays.length === 0;
  return (
    <WidgetCard
      title={t("agenda.title")}
      action={newAppointment}
      loading={items.isPending}
      empty={empty}
      emptyState={{ message: t("agenda.nothingThisWeek"), action: openCalendar }}
    >
      {items.isError ? (
        <Text size="sm" c="red" role="alert" mt="md">
          {t("agenda.failed")}
        </Text>
      ) : (
        <Stack gap="md" mt="md">
          <Stack gap="xs" aria-label={t("today")} role="region">
            <Text size="xs" fw={600} tt="uppercase" c="dimmed">
              {t("agenda.todayHeading", { date: dayLabel(today) })}
            </Text>
            {itemsOn(today).length === 0 && holidaysOn(today).length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("agenda.nothingToday")}
              </Text>
            ) : (
              list(today)
            )}
          </Stack>
          <Stack gap={4}>
            {days.slice(1).map((day) => {
              const count = itemsOn(day).length;
              const open = expanded.has(day);
              return (
                <Stack key={day} gap={4}>
                  <UnstyledButton onClick={() => toggle(day)} aria-expanded={open} data-testid="agenda-day">
                    <Group gap={6} wrap="nowrap">
                      {open ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
                      <Text size="sm">{t("agenda.dayLine", { day: dayLabel(day), count })}</Text>
                    </Group>
                  </UnstyledButton>
                  {open && <div style={{ paddingLeft: 20 }}>{list(day)}</div>}
                </Stack>
              );
            })}
          </Stack>
          {openCalendar}
        </Stack>
      )}
    </WidgetCard>
  );
}
