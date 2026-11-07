import { createFileRoute } from "@tanstack/react-router";
import { KnowledgeHomePage } from "@vantigo/knowledge-ui/pages/home";

/**
 * The app's home (knowledge design K4): the sidebar's tree and the start
 * page. It is knowledge:articles', which is what the guard asks of the whole
 * prefix; the spotlight's New article lands here with `new=1`.
 */
export const Route = createFileRoute("/knowledge/")({
  validateSearch: (search: Record<string, unknown>): { new?: number } => (search.new === 1 ? { new: 1 } : {}),
  component: KnowledgeHomePage,
});
