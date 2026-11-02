import type { AssistTarget } from "@vantigo/frontend-shell";
import targets from "./assist-targets.json";
import "./i18n";

export * from "./api/account-lifecycle";
export * from "./api/attachments";
export * from "./api/auth";
export * from "./api/contacts";
export * from "./api/customers";
export * from "./api/imports";
export * from "./api/lookup";
export * from "./api/timeline";
export { ContactDetailsPage } from "./pages/contacts.$contactId";
export { ContactsPage } from "./pages/contacts.index";
export { CustomerDetailHeader, CustomerOverview } from "./pages/customers.$customerId";
export { CustomersPage } from "./pages/customers.index";
export { ImportPage } from "./pages/import.$importId";
export { ImportStartPage } from "./pages/import.index";

/** The controls the Assistant can point at in a tour; each carries its key as a data-assist attribute. */
export const assistTargets: AssistTarget[] = targets;
