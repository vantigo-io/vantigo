import { useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { ProjectExpensesPanel } from "@vantigo/expenses-ui/components/project-expenses-panel";
import { useI18n } from "@vantigo/frontend-shell";
import { ModuleTabGate } from "./-module-tab-gate";
import "../../i18n";

/**
 * The project page's Expenses tab. It lives on the projects page but calls the
 * expenses API, so it carries that module's own two gates (`ModuleTabGate`):
 * the tab is hidden without them, and a pasted link must reach neither an API
 * that is not mounted nor one that refuses every read. Who may see the
 * project's totals, and which of the expenses behind them, is the panel's own
 * question — the expenses API answers a bare 404 for the figures and shapes
 * the list by its own visibility rule.
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
  return (
    <ModuleTabGate module="expenses" permission="expenses:access" appLabel={t("navigation.expenses")}>
      {/* Keyed on the project: the panel keeps its filter, its page and its
          open drawer in local state, and history-navigating between two
          projects' Expenses tabs renders the same component for a different
          id — which would otherwise show one project's rows under the
          other's heading while it loads. */}
      <ProjectExpensesPanel
        key={projectId}
        projectId={projectId}
        onChanged={() => void queryClient.invalidateQueries({ queryKey: ["projects", "economy"] })}
      />
    </ModuleTabGate>
  );
};
