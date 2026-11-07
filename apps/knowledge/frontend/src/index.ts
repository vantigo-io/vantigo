import "./i18n";

export * from "./api/request";
export { assistTargets } from "./assist-targets";
export { knowledgeCatalog } from "./i18n";
export { type KnowledgePageAccess, noKnowledgeAccess } from "./lib/access";
export { type KnowledgeNavItem, knowledgeNavItems, visibleKnowledgeNavItems } from "./lib/navigation";
export * from "./lib/routes";
export { ArticlePage } from "./pages/article";
export { KnowledgeHomePage } from "./pages/home";
export { TrashPage } from "./pages/trash";
