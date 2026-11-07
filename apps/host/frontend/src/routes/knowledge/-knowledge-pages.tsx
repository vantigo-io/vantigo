import { useParams } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { ArticlePage } from "@vantigo/knowledge-ui/pages/article";
import { TrashPage } from "@vantigo/knowledge-ui/pages/trash";
import { useKnowledgeAccess } from "../../lib/knowledge-access";

/**
 * The Knowledge pages that need more than the route gives them. They sit
 * beside the route files because a route file may export nothing but its
 * `Route`.
 */
export const ArticleRoutePage = () => {
  const { articleId } = useParams({ from: "/knowledge/articles/$articleId" });
  return <ArticlePage articleId={articleId} />;
};

/** The trash, told whether the caller may empty it (knowledge:manage, K10). */
export const TrashRoutePage = () => {
  const { ready, access } = useKnowledgeAccess();
  if (!ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return <TrashPage access={access} />;
};
