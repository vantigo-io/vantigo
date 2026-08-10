import { createFileRoute } from "@tanstack/react-router";
import { MailboxesPage } from "@vantigo/communications-ui/pages/admin.mailboxes";
export const Route = createFileRoute("/communications/mailboxes")({ component: MailboxesPage });
