import { createFileRoute } from "@tanstack/react-router";
import { validateEconomyPortfolioSearch } from "@vantigo/projects-ui/lib/economy";
import { EconomyPortfolio } from "@vantigo/projects-ui/pages/portfolio";

// A static segment beside /projects/$projectId, the way my-tasks.tsx sits
// beside it; TanStack ranks the literal segment above the dynamic one, so this
// page is never asked for as the project "economy". The package owns its own
// search vocabulary (status defaults to "active" here, unlike the bare project
// list, and "all" is the only way to lift it), so the route hands the
// validator straight through rather than hand-rolling it.
export const Route = createFileRoute("/projects/economy")({
  validateSearch: validateEconomyPortfolioSearch,
  component: EconomyPortfolio,
});
