import "./i18n";

export * from "./api/access";
export * from "./api/customers";
export * from "./api/lines";
export * from "./api/people";
export * from "./api/products";
export * from "./api/projects";
export { CustomerPicker, type CustomerPickerProps, type CustomerPickerValue } from "./components/customer-picker";
export {
  CustomerProjectsPanel,
  type CustomerProjectsPanelProps,
} from "./components/customer-projects-panel";
export { ProjectStatusBadge, type ProjectStatusBadgeProps } from "./components/project-status-badge";
export { projectsCatalog } from "./i18n";
export * from "./lib/billing";
export { holdsPermission } from "./lib/permissions";
export * from "./lib/roles";
export * from "./lib/status";
export { type CodeSuggestion, useCodeSuggestion } from "./lib/use-code-suggestion";
export {
  BillingLineFormModal,
  type BillingLineFormModalProps,
  type BillingLineModalState,
} from "./pages/-billing-line-form-modal";
export { ProjectFormModal, type ProjectModalState } from "./pages/-project-form-modal";
export { ProjectTimeline } from "./pages/-project-timeline";
export { ProjectBilling } from "./pages/project-billing";
export { ProjectPeople } from "./pages/project-people";
export { ProjectDetailHeader, ProjectOverview } from "./pages/projects.$projectId";
export { ProjectsPage, type ProjectsSearch } from "./pages/projects.index";
