import { useParams } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { UninvoicedWorkPanel } from "@vantigo/invoices-ui";
import { ModuleTabGate } from "./-module-tab-gate";
import "../../i18n";

/**
 * The project page's Invoicing tab (invoices work design D18): the project's
 * work not yet invoiced, and the wizard that makes an invoice draft of it. It
 * lives on the projects page but calls the invoices API, so it carries that
 * module's own two gates (`ModuleTabGate`): the tab is hidden without the
 * invoices module or without `invoices:create` — the view lists the hours,
 * the people and the rates an invoice will state, so it is the drafter's —
 * and a pasted link reaches neither an API that is not mounted nor one that
 * refuses every read.
 *
 * It sits beside the route file rather than inside it because a route file may
 * export nothing but its `Route` without costing the bundle a code split.
 */
export const ProjectInvoicingTab = () => {
  const { t } = useI18n("host");
  const { projectId } = useParams({ from: "/projects/$projectId" });
  return (
    <ModuleTabGate module="invoices" permission="invoices:create" appLabel={t("navigation.invoices")}>
      {/* Keyed on the project: the panel keeps what is chosen in local state. */}
      <UninvoicedWorkPanel key={projectId} projectId={projectId} />
    </ModuleTabGate>
  );
};
