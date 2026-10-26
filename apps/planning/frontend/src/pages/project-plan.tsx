import { Badge, Group, Stack } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useParams, useSearch } from "@tanstack/react-router";
import { PageHeader } from "@vantigo/frontend-shell";
import { projectPlanQueryOptions } from "../api/plan";
import { ProjectPlan } from "../components/project-plan";
import { usePlanningFormat } from "../lib/format";
import { BOARD_ROUTE_PATH } from "../lib/routes";
import type { WindowSearch } from "../lib/search";
import { planWindow } from "../lib/window";

/** A project status's badge colour. */
const statusColor: Record<string, string> = {
  planned: "violet",
  active: "teal",
  "on-hold": "yellow",
  completed: "gray",
  cancelled: "red",
};

/**
 * One project's plan as a page of this app (design S11, §7): the header — the
 * project, its status, its customer and dates — and the same `ProjectPlan`
 * the host puts on the project page's Planning tab. It reads the same query,
 * so the header costs no second request.
 */
export const ProjectPlanPage = () => {
  const format = usePlanningFormat();
  const { t } = format;
  const { projectId } = useParams({ strict: false }) as { projectId: string };
  const search = useSearch({ strict: false }) as WindowSearch;
  const id = Number(projectId);
  const window = planWindow(search);
  const plan = useQuery(projectPlanQueryOptions(id, window));
  const project = plan.data?.project;
  const name = project ? `${project.code} ${project.name}` : t("projectById", { id });

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("navBoard"), to: BOARD_ROUTE_PATH }, { label: name }]}
        title={
          <Group gap="sm">
            <span>{name}</span>
            {project && (
              <Badge variant="light" color={statusColor[project.status]}>
                {t(`projectStatus_${project.status}`)}
              </Badge>
            )}
          </Group>
        }
        description={
          project
            ? [
                project.customer?.name,
                project.startDate || project.endDate
                  ? t("projectDates", {
                      from: project.startDate ? format.date(project.startDate) : "…",
                      to: project.endDate ? format.date(project.endDate) : "…",
                    })
                  : undefined,
              ]
                .filter(Boolean)
                .join(" · ")
            : undefined
        }
      />
      <ProjectPlan projectId={id} from={window.from} to={window.to} />
    </Stack>
  );
};
