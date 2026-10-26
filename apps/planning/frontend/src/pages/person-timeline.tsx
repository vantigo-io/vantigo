import { Stack } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useParams, useSearch } from "@tanstack/react-router";
import { PageHeader } from "@vantigo/frontend-shell";
import { planningMetaQueryOptions } from "../api/meta";
import { personTimelineQueryOptions } from "../api/timeline";
import { PersonTimeline } from "../components/person-timeline";
import { usePlanningFormat } from "../lib/format";
import { BOARD_ROUTE_PATH } from "../lib/routes";
import type { WindowSearch } from "../lib/search";
import { timelineWindow } from "../lib/window";

/**
 * A person's timeline as a page of this app (design S12, §7): the header, and
 * the same `PersonTimeline` the host puts on Staff's person page. It reads the
 * same query, so the header's name costs no second request.
 */
export const PersonTimelinePage = () => {
  const { t } = usePlanningFormat();
  const { personId } = useParams({ strict: false }) as { personId: string };
  const search = useSearch({ strict: false }) as WindowSearch;
  const id = Number(personId);
  const window = timelineWindow(search);
  const timeline = useQuery(personTimelineQueryOptions(id, window));
  const meta = useQuery(planningMetaQueryOptions());
  const person = timeline.data?.person;
  const name = person?.displayName ?? t("personById", { id });
  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={meta.data?.canView ? [{ label: t("navBoard"), to: BOARD_ROUTE_PATH }, { label: name }] : undefined}
        title={t("timelineTitle", { name })}
        description={person ? [person.employeeNumber, person.department?.name].filter(Boolean).join(" · ") : undefined}
      />
      <PersonTimeline personId={id} from={window.from} to={window.to} />
    </Stack>
  );
};
