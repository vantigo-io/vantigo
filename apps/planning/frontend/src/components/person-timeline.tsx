import { Alert, Badge, Card, Group, ScrollArea, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { PlanningAllocation } from "../api/allocations";
import { planningMetaQueryOptions } from "../api/meta";
import { NotFoundError } from "../api/request";
import { personTimelineQueryOptions } from "../api/timeline";
import { NONE, usePlanningFormat } from "../lib/format";
import { mondayOf, weeksBetween } from "../lib/weeks";
import { timelineWindow } from "../lib/window";
import { AllocationBars, SpanLane } from "./allocation-bars";
import { AllocationSheet } from "./allocation-sheet";
import { WeekHeader } from "./week-header";
import { useWishText } from "./wish-hooks";

export interface PersonTimelineProps {
  personId: number;
  /** The window; left out, today and the twenty-six weeks after it (S12). */
  from?: string;
  to?: string;
}

const WEEK_WIDTH = 64;

/** Whether a row's terms move its release past its end, or set a handover (S15). */
const hasMarks = (allocation: PlanningAllocation) =>
  allocation.earliestRelease !== allocation.toDate || allocation.handoverStart !== undefined;

/**
 * One person's forward view (design S12, §7): the employment, the days away,
 * each allocation as a bar over the weeks, and the flags — the days booked
 * past the employment's end, and a held certificate that expires inside a
 * booking — and, from delivery C, the earliest release and the handover start
 * an assignment's terms derive (S15), marked on a lane of their own and
 * listed in the table, and the person's open wishes (S21) to those who may
 * read them. It takes nothing from the route, so the host can put it on
 * Staff's person page as well as on this app's own timeline page.
 *
 * The person's own timeline (`planning:access` alone) is their confirmed
 * rows and nothing else (amendment 2): the employment and the days away are
 * Staff's to show them, on My page.
 */
export const PersonTimeline = ({ personId, from, to }: PersonTimelineProps) => {
  const format = usePlanningFormat();
  const { t } = format;
  const window = timelineWindow({ from, to });
  const timeline = useQuery(personTimelineQueryOptions(personId, window));
  const meta = useQuery(planningMetaQueryOptions());
  const [editing, setEditing] = useState<PlanningAllocation>();
  const wishText = useWishText();
  const canManage = meta.data?.canManage === true;
  const weeks = weeksBetween(window.from, window.to);
  const thisWeek = mondayOf(window.from);

  if (timeline.isPending) return <ContentSkeleton rows={3} rowHeight={40} />;
  // A person the plan does not answer for these weeks — Staff no longer
  // shows them, or the self audience asking for someone else — is no failure.
  if (timeline.error instanceof NotFoundError) return <EmptyState size="sm" title={t("notInPlanTitle")} />;
  if (timeline.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadTimeline")}>
        {timeline.error.message}
      </Alert>
    );
  const data = timeline.data;
  const self = meta.data !== undefined && !meta.data.canView;
  const flagsOf = (allocationId: number) => data.flags.filter((flag) => flag.allocationId === allocationId);
  const marked = data.allocations.filter(hasMarks);

  return (
    <Stack gap="md">
      <Card withBorder padding="md" radius="md">
        <ScrollArea type="auto">
          <Stack gap={6} miw={weeks.length * WEEK_WIDTH}>
            <div style={{ display: "grid", gridTemplateColumns: `repeat(${weeks.length}, minmax(0, 1fr))` }}>
              {weeks.map((monday) => (
                <WeekHeader key={monday} monday={monday} current={monday === thisWeek} />
              ))}
            </div>
            {data.employed.length > 0 && (
              <SpanLane
                label={t("employment")}
                weeks={weeks}
                spans={data.employed.map((span) => ({
                  key: `${span.from}/${span.to}`,
                  span,
                  color: "var(--mantine-color-gray-light)",
                  title: t("employedSpan", { range: format.range(span.from, span.to) }),
                  content: (
                    <Text size="xs" c="dimmed" truncate>
                      {t("employment")}
                    </Text>
                  ),
                }))}
              />
            )}
            {data.away.length > 0 && (
              <SpanLane
                label={t("daysAway")}
                weeks={weeks}
                spans={data.away.map((span) => ({
                  key: `${span.from}/${span.to}`,
                  span,
                  color: "var(--mantine-color-red-light)",
                  title: t("awaySpan", { range: format.range(span.from, span.to) }),
                  content: (
                    <Text size="xs" c="red" truncate>
                      {t("away")}
                    </Text>
                  ),
                }))}
              />
            )}
            <AllocationBars
              allocations={data.allocations}
              weeks={weeks}
              label={t("allocations")}
              name={(allocation) => format.projectName(allocation.project)}
              onOpen={canManage ? setEditing : undefined}
            />
            {marked.length > 0 && (
              <SpanLane
                label={t("releaseMarks")}
                weeks={weeks}
                spans={marked.flatMap((allocation) => [
                  ...(allocation.earliestRelease !== allocation.toDate
                    ? [
                        {
                          key: `${allocation.id}/release`,
                          span: { from: allocation.earliestRelease, to: allocation.earliestRelease },
                          color: "var(--mantine-color-grape-light)",
                          title: `${format.projectName(allocation.project)}: ${t("earliestReleaseOn", { date: format.date(allocation.earliestRelease) })}`,
                          content: (
                            <Text size="xs" c="grape" truncate>
                              {t("earliestRelease")}
                            </Text>
                          ),
                        },
                      ]
                    : []),
                  ...(allocation.handoverStart
                    ? [
                        {
                          key: `${allocation.id}/handover`,
                          span: { from: allocation.handoverStart, to: allocation.handoverStart },
                          color: "var(--mantine-color-teal-light)",
                          title: `${format.projectName(allocation.project)}: ${t("handoverFromOn", { date: format.date(allocation.handoverStart) })}`,
                          content: (
                            <Text size="xs" c="teal" truncate>
                              {t("handoverFrom")}
                            </Text>
                          ),
                        },
                      ]
                    : []),
                ])}
              />
            )}
          </Stack>
        </ScrollArea>
        {self && (
          <Text size="xs" c="dimmed" mt="sm">
            {t("selfTimelineNote")}
          </Text>
        )}
      </Card>

      {data.wishes.length > 0 && (
        <Card withBorder padding="md" radius="md">
          <Text fw={600} mb="xs">
            {t("openWishes")}
          </Text>
          <Stack component="ul" gap={6} m={0} p={0} style={{ listStyle: "none" }} aria-label={t("openWishes")}>
            {data.wishes.map((wish) => (
              <Group component="li" key={wish.id} gap="xs" wrap="wrap">
                <Text size="sm">{wishText(wish)}</Text>
                <Text size="xs" c="dimmed">
                  {wish.seenAt ? t("seenOn", { date: format.stamp(wish.seenAt) }) : t("notSeenYet")}
                </Text>
              </Group>
            ))}
          </Stack>
        </Card>
      )}

      {data.allocations.length === 0 ? (
        <EmptyState size="sm" title={t("noAllocationsInWindow")} />
      ) : (
        <Table.ScrollContainer minWidth={720}>
          <Table verticalSpacing="xs" aria-label={t("allocations")}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("project")}</Table.Th>
                <Table.Th>{t("period")}</Table.Th>
                <Table.Th>{t("load")}</Table.Th>
                <Table.Th>{t("status")}</Table.Th>
                <Table.Th>{t("earliestRelease")}</Table.Th>
                <Table.Th>{t("handoverFrom")}</Table.Th>
                <Table.Th>{t("flags")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {data.allocations.map((allocation) => (
                <Table.Tr key={allocation.id}>
                  <Table.Td>
                    <Text size="sm" fw={500}>
                      {format.projectName(allocation.project)}
                    </Text>
                    {allocation.title && (
                      <Text size="xs" c="dimmed">
                        {allocation.title}
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>{format.range(allocation.fromDate, allocation.toDate)}</Table.Td>
                  <Table.Td>{format.load(allocation.load, allocation.loadUnit)}</Table.Td>
                  <Table.Td>
                    <Badge variant="light" color={allocation.status === "confirmed" ? "blue" : "violet"} size="sm">
                      {t(`status_${allocation.status}`)}
                    </Badge>
                  </Table.Td>
                  <Table.Td>
                    {allocation.earliestRelease !== allocation.toDate ? format.date(allocation.earliestRelease) : NONE}
                  </Table.Td>
                  <Table.Td>{allocation.handoverStart ? format.date(allocation.handoverStart) : NONE}</Table.Td>
                  <Table.Td>
                    <Group gap={4}>
                      {flagsOf(allocation.id).map((flag) => (
                        <Badge key={`${flag.code}/${flag.date ?? ""}`} color="orange" variant="light" size="sm">
                          {t(`flag_${flag.code}`, { date: flag.date ? format.date(flag.date) : "" })}
                        </Badge>
                      ))}
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
      {canManage && (
        <AllocationSheet opened={editing !== undefined} onClose={() => setEditing(undefined)} allocation={editing} />
      )}
    </Stack>
  );
};
