import "./i18n";

export * from "./api/mailboxes";
export * from "./api/messages";
export * from "./api/suppressions";
export { communicationsCatalog } from "./i18n";
export { MailboxesPage } from "./pages/admin.mailboxes";
export { SuppressionsPage } from "./pages/admin.suppressions";
export { DetailPage as MessageDetailsPage } from "./pages/messages.$messageId";
export { ComposePage } from "./pages/messages.compose";
export { MessagesPage } from "./pages/messages.index";
