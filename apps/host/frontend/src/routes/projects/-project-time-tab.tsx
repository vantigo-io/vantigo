import { useParams } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { ProjectTimePanel } from "@vantigo/time-ui/components/project-time-panel";
import { ModuleTabGate } from "./-module-tab-gate";
import "../../i18n";

/**
 * The project page's Time tab. It lives in the projects app but calls the time
 * API, so it carries that module's own two gates (`ModuleTabGate`): the tab is
 * hidden without them, and a pasted link must reach neither an API that is not
 * mounted nor one that refuses every read. Whether this caller may see the
 * project's hours at all is the panel's own question — the time API answers a
 * bare 404 for a project the caller may not see.
 *
 * It sits beside the route file rather than inside it because a route file may
 * export nothing but its `Route` without costing the bundle a code split.
 */
export const ProjectTimeTab = () => {
  const { t } = useI18n("host");
  const { projectId } = useParams({ from: "/projects/$projectId" });
  return (
    <ModuleTabGate module="time" permission="time:access" appLabel={t("navigation.time")}>
      <ProjectTimePanel projectId={projectId} />
    </ModuleTabGate>
  );
};
