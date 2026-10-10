import { useParams } from "@tanstack/react-router";
import { InvoicePage } from "../pages/invoice";

/**
 * The document page as a route component, the way the host mounts it: the
 * route owns the path parameter and hands the id over as a number.
 */
export const InvoiceRoute = ({
  canViewCustomers,
  currentUserId,
}: {
  canViewCustomers: boolean;
  currentUserId?: string;
}) => {
  const { invoiceId } = useParams({ strict: false }) as { invoiceId: string };
  return (
    <InvoicePage invoiceId={Number(invoiceId)} canViewCustomers={canViewCustomers} currentUserId={currentUserId} />
  );
};
