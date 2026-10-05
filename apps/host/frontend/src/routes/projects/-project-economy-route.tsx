import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { projectQueryOptions } from "@vantigo/projects-ui/api/projects";
import { ProjectEconomy } from "@vantigo/projects-ui/pages/project-economy";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { useInvoiceHref } from "../../lib/invoice-access";
import { visibleProjectDetailTabs } from "./-project-detail-layout";

/**
 * The Economy tab. It is shown to everyone who sees the project; the page
 * itself shapes its content per caller (hours only, budget amounts, cost and
 * margin) from what the economy endpoint returns, and refuses an outsider with
 * its own locked state.
 *
 * `expensesHref` is the one thing the projects package cannot know: where this
 * project's expenses live in the host's routes. It is handed over exactly when
 * the **Expenses tab itself is open to this caller** — which is the module
 * being mounted *and* `expenses:access`, the permission every operation of
 * that API demands. The answer is `visibleProjectDetailTabs`, the same
 * function the tab row is built from, so the link and the tab can never give
 * two different answers. The row it belongs to is already hidden unless the
 * server reports lines ready to invoice.
 *
 * `invoicingHref` is the Invoicing tab's (invoices work design D18), by the
 * same rule: handed over exactly when that tab is open to this caller — the
 * invoices module and `invoices:create`. `invoiceHref` is where one invoice
 * lives, for a milestone's "Invoiced by invoice n", when the caller may read
 * invoices at all.
 */
export const ProjectEconomyRoute = () => {
  const { projectId } = useParams({ from: "/projects/$projectId" });
  // The layout above asked for all three of these, so they are read from its
  // cache rather than fetched again.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  const project = useQuery(projectQueryOptions(projectId));
  const tabs = visibleProjectDetailTabs(
    enabledModuleKeys(),
    authorization.data?.permissions,
    project.data?.capabilities,
  );
  const expenses = tabs.some((tab) => tab.value === "expenses");
  const invoicing = tabs.some((tab) => tab.value === "invoicing");
  const invoiceHref = useInvoiceHref();
  return (
    <ProjectEconomy
      projectId={projectId}
      expensesHref={expenses ? `/projects/${projectId}/expenses` : undefined}
      invoicingHref={invoicing ? `/projects/${projectId}/invoicing` : undefined}
      invoiceHref={invoiceHref}
    />
  );
};
