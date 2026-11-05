import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@vantigo/invoices-ui/pages/settings";
import { useInvoiceAccess } from "../../lib/invoice-access";

export const Route = createFileRoute("/invoices/settings")({
  component: SettingsRoute,
});

/**
 * The settings, told who is signed in so a regime review or a rate of theirs
 * says "you", and whether Products' tax categories may be named.
 */
function SettingsRoute() {
  const access = useInvoiceAccess();
  return <SettingsPage currentUserId={access.userId} canReadTaxCategories={access.canReadTaxCategories} />;
}
