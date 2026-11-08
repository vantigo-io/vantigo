import { createFileRoute, notFound } from "@tanstack/react-router";
import { parseAgentTab } from "@vantigo/agents-ui/lib/routes";
import { AgentRoutePage } from "./-agents-pages";
import { isUuid } from "./-ids";

/** One agent; the tab lives in the `tab` search parameter. A path that is not a uuid is the layout's not-found. */
export const Route = createFileRoute("/agents/$agentId/")({
  validateSearch: (search: Record<string, unknown>) => ({ tab: parseAgentTab(search.tab) }),
  beforeLoad: ({ params }) => {
    if (!isUuid(params.agentId)) throw notFound();
  },
  component: AgentRoutePage,
});
