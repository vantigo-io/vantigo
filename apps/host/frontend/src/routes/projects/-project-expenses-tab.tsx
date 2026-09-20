import { useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { ProjectExpensesPanel } from "@vantigo/expenses-ui/components/project-expenses-panel";
import { useI18n } from "@vantigo/frontend-shell";
import { ModuleNotEnabledPage } from "../../components/errors";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import "../../i18n";

/**
 * The project page's Expenses tab. It lives on the projects page but calls the
 * expenses API, so it gates on the expenses module itself: the tab is hidden
 * without it, but a pasted link must not hit an API that is not mounted. Who
 * may see the project's totals, and which of the expenses behind them, is the
 * panel's own question — the expenses API answers a bare 404 for the figures
 * and shapes the list by its own visibility rule.
 *
 * The host is also the only place that may refresh the **Economy** tab's
 * figures when something here changes them. Both tabs read the same expenses,
 * one through the expenses API and one through the projects API, and no module
 * package may import another's query keys — so the panel reports a change and
 * the host invalidates `["projects", "economy"]`.
 *
 * It sits beside the route file rather than inside it because a route file may
 * export nothing but its `Route` without costing the bundle a code split.
 */
export const ProjectExpensesTab = () => {
  const { t } = useI18n("host");
  const { projectId } = useParams({ from: "/projects/$projectId" });
  const queryClient = useQueryClient();
  if (!enabledModuleKeys().includes("expenses")) return <ModuleNotEnabledPage appLabel={t("navigation.expenses")} />;
  return (
    <ProjectExpensesPanel
      projectId={projectId}
      onChanged={() => void queryClient.invalidateQueries({ queryKey: ["projects", "economy"] })}
    />
  );
};
