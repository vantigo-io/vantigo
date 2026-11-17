import { createFileRoute } from "@tanstack/react-router";
import { validateProposalsSearch } from "@vantigo/purchasing-ui";
import { ProposalsRoutePage } from "./-purchasing-pages";

/** The purchase proposals (P31–P35), filtered to a variant and a site when Order… links in. */
export const Route = createFileRoute("/purchasing/proposals")({
  validateSearch: validateProposalsSearch,
  component: ProposalsRoutePage,
});
