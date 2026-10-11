import { Alert, Group, Pagination, Paper, Stack, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { myEventsQueryOptions } from "../api/me";
import type { StaffStructure } from "../api/meta";
import { orgQueryOptions, type StaffPerson, type StaffPersonSummary } from "../api/people";
import "../i18n";
import { formatCalendarDate } from "../lib/dates";
import { type EventFormatting, eventLines } from "../lib/events";

export interface PersonHistoryProps {
  /** One page of the history — a person's, or the caller's own on My page. */
  page: (page: number) => ReturnType<typeof myEventsQueryOptions>;
  /** Whose history it is: they and the people around them are who a change can be named by. */
  person: StaffPerson;
  /** The structure, when the reader holds it, to name a department, a location or a unit. */
  structure?: StaffStructure;
}

/**
 * The History tab (design S21): every change, newest first, paged, each
 * with who made it and when, and its fields as the server shaped them for
 * this reader — values before and after, "changed" where the group is not
 * theirs, or the kind alone when nothing of it is (a pay correction to a
 * manager). The page never decides what a reader may see of a change.
 */
export const PersonHistory = ({ page: pageQuery, person, structure }: PersonHistoryProps) => {
  const { t, i18n, formatters } = useI18n("staff");
  const [page, setPage] = useState(1);
  const { data, isPending, isError, error } = useQuery(pageQuery(page));
  // The people this page knows by name: the person, their manager today,
  // the chain above them and their reports.
  const { data: org } = useQuery(orgQueryOptions(person.id));
  const people = new Map<number, StaffPersonSummary>();
  for (const one of [person, person.manager, ...(org?.chain ?? []), ...(org?.reports ?? [])])
    if (one) people.set(one.id, one);
  const name = (field: string, id: number): string | undefined => {
    if (field === "managerPersonId") return people.get(id)?.displayName;
    const rows =
      field === "departmentId"
        ? structure?.departments
        : field === "locationId"
          ? structure?.locations
          : field === "businessUnitId"
            ? structure?.businessUnits
            : undefined;
    return rows?.find((row) => row.id === id)?.name;
  };

  const formatting: EventFormatting = {
    t: (key, options) => t(key, options),
    has: (key) => i18n.exists(key, { ns: "staff" }),
    date: (value) => formatCalendarDate(value, formatters.formatDate),
    number: (value) => formatters.formatNumber(value, { maximumFractionDigits: 2 }),
    name,
  };

  if (isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadHistory")}>
        {error.message}
      </Alert>
    );
  if (isPending) return null;
  if (data.data.length === 0) return <EmptyState title={t("noHistory")} />;
  return (
    <Stack gap="sm">
      {data.data.map((event) => {
        const lines = eventLines(event.payload, formatting);
        const kind = i18n.exists(`event_${event.kind}`, { ns: "staff" }) ? t(`event_${event.kind}`) : event.kind;
        return (
          <Paper key={event.id} component="article" withBorder radius="md" p="md" aria-label={kind}>
            <Stack gap={4}>
              <Group justify="space-between" wrap="wrap" gap="xs">
                <Text size="sm" fw={600}>
                  {kind}
                </Text>
                <Text size="xs" c="dimmed">
                  {formatters.formatDate(new Date(event.at), { dateStyle: "medium", timeStyle: "short" })}
                </Text>
              </Group>
              <Text size="xs" c="dimmed">
                {event.actor.displayName}
              </Text>
              {lines.map((line, index) => (
                <Text key={index} size="sm">
                  {line}
                </Text>
              ))}
            </Stack>
          </Paper>
        );
      })}
      {data.pagination.totalPages > 1 && (
        <Group justify="center">
          <Pagination total={data.pagination.totalPages} value={page} onChange={setPage} />
        </Group>
      )}
    </Stack>
  );
};
