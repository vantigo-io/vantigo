import "./i18n";

export * from "./api/access";
export * from "./api/customers";
export * from "./api/lines";
export * from "./api/people";
export * from "./api/products";
export * from "./api/projects";
export { CustomerPicker, type CustomerPickerProps, type CustomerPickerValue } from "./components/customer-picker";
export { ProjectStatusBadge, type ProjectStatusBadgeProps } from "./components/project-status-badge";
export { projectsCatalog } from "./i18n";
export * from "./lib/billing";
export { holdsPermission } from "./lib/permissions";
export * from "./lib/roles";
export * from "./lib/status";
export { type CodeSuggestion, useCodeSuggestion } from "./lib/use-code-suggestion";
export { ProjectFormModal, type ProjectModalState } from "./pages/-project-form-modal";
export { ProjectsPage, type ProjectsSearch } from "./pages/projects.index";
