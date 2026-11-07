/** The app's paths (knowledge design K1), which the host mounts and the package's links name. */
export const KNOWLEDGE_HOME_ROUTE_PATH = "/knowledge";
export const ARTICLE_ROUTE_PATH = "/knowledge/articles/$articleId";
export const TRASH_ROUTE_PATH = "/knowledge/trash";

/** An article's page, the link the Assistant cites too (K15). */
export const articleLinkOptions = (articleId: string) => ({ to: ARTICLE_ROUTE_PATH, params: { articleId } });
