import { createFileRoute, notFound } from "@tanstack/react-router";
import { ArticlePage } from "@vantigo/knowledge-ui/pages/article";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** One article (knowledge design K3), the link the Assistant cites (K15). A path that is not a uuid is the layout's not-found. */
export const Route = createFileRoute("/knowledge/articles/$articleId")({
  beforeLoad: ({ params }) => {
    if (!UUID.test(params.articleId)) throw notFound();
  },
  component: ArticlePage,
});
