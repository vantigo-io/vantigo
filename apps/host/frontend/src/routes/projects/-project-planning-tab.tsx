import { useParams } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { ProjectPlan } from "@vantigo/planning-ui/components/project-plan";
import { ModuleTabGate } from "./-module-tab-gate";
import "../../i18n";

/**
 * The project page's Planning tab (planning design §7): the project's plan —
 * its people week by week, its requests and their demand — composed by the
 * host from the Planning package's `ProjectPlan`, as the Time tab composes
 * Time's panel. It calls the planning API, so it carries that module's own
 * gates (`ModuleTabGate`): the module mounted, and both `planning:access` and
 * `planning:view`, since every Planning call needs the first and the plan the
 * second, which `planning:manage` does not imply. A pasted link without them
 * reaches neither an API that is not mounted nor one that refuses the read.
 *
 * It sits beside the route file rather than inside it because a route file may
 * export nothing but its `Route` without costing the bundle a code split.
 */
export const ProjectPlanningTab = () => {
  const { t } = useI18n("host");
  const { projectId } = useParams({ from: "/projects/$projectId" });
  return (
    <ModuleTabGate
      module="planning"
      permission={["planning:access", "planning:view"]}
      appLabel={t("navigation.planning")}
    >
      <ProjectPlan projectId={projectId} />
    </ModuleTabGate>
  );
};
