import { useParams } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { ProjectTimePanel } from "@vantigo/time-ui/components/project-time-panel";
import { ModuleNotEnabledPage } from "../../components/errors";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import "../../i18n";

/**
 * The project page's Time tab. It lives in the projects app but calls the time
 * API, so it gates on the time module itself: the tab is hidden without it,
 * but a pasted link must not hit an API that is not mounted. Whether this
 * caller may see the project's hours at all is the panel's own question — the
 * time API answers a bare 404 for a project the caller may not see.
 *
 * It sits beside the route file rather than inside it because a route file may
 * export nothing but its `Route` without costing the bundle a code split.
 */
export const ProjectTimeTab = () => {
  const { t } = useI18n("host");
  const { projectId } = useParams({ from: "/projects/$projectId" });
  if (!enabledModuleKeys().includes("time")) return <ModuleNotEnabledPage appLabel={t("navigation.time")} />;
  return <ProjectTimePanel projectId={projectId} />;
};
