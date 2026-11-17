import { createFileRoute, notFound } from "@tanstack/react-router";
import { SupplierInvoicePage } from "@vantigo/accounting-ui/pages/supplier-invoices";
import { PurchaseOrderPanel } from "@vantigo/purchasing-ui";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { usePurchasingAccess } from "../../lib/purchasing-access";
import { hasAllPermissions } from "../../navigation";
import { isPositiveId } from "./-ids";

/**
 * One supplier invoice (accounting design A18, A19), with the panels other
 * modules add: Accounting never names Purchasing, so this route passes its
 * **Purchase order** panel (purchasing design P37, A20) through the page's
 * `extraPanels` slot, when Purchasing is on and the caller holds
 * purchasing:access. The panel itself shows the invoice's lines only to a
 * holder of accounting:read, as Purchasing answers them, and offers the match
 * to a holder of purchasing:invoices-match.
 */
const SupplierInvoiceRoutePage = () => {
  const purchasingOn = enabledModuleKeys().includes("purchasing");
  const { ready, access, permissions } = usePurchasingAccess();
  const panel =
    purchasingOn && ready && hasAllPermissions(permissions, ["purchasing:access"])
      ? (invoiceId: number) => <PurchaseOrderPanel invoiceId={invoiceId} canMatch={access.canMatchInvoices === true} />
      : undefined;
  return <SupplierInvoicePage extraPanels={panel} />;
};

/** A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/accounting/supplier-invoices/$supplierInvoiceId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.supplierInvoiceId)) throw notFound();
  },
  component: SupplierInvoiceRoutePage,
});
