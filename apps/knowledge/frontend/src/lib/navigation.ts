import { KNOWLEDGE_HOME_ROUTE_PATH, TRASH_ROUTE_PATH } from "./routes";

/** One sidebar destination of the app; the host owns its label and icon. */
export interface KnowledgeNavItem {
  key: "articles" | "trash";
  /** The key of the destination's label in the package catalog. */
  label: string;
  to: string;
  /** Any one of these offers the entry (knowledge design §6). */
  requiredPermissions: readonly string[];
}

/** Every Knowledge call asks for knowledge:articles; each article's own access decides the rest (K2, K4). */
const articles = ["knowledge:articles"] as const;

export const knowledgeNavItems: readonly KnowledgeNavItem[] = [
  { key: "articles", label: "navArticles", to: KNOWLEDGE_HOME_ROUTE_PATH, requiredPermissions: articles },
  { key: "trash", label: "navTrash", to: TRASH_ROUTE_PATH, requiredPermissions: articles },
];

/** The entries the caller's permissions offer. */
export const visibleKnowledgeNavItems = (permissions: readonly string[] | undefined) =>
  knowledgeNavItems.filter(
    (item) => permissions?.includes("*") === true || item.requiredPermissions.some((p) => permissions?.includes(p)),
  );
