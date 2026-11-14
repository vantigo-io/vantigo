import { Alert, Anchor } from "@mantine/core";
import { IconInfoCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import { metaQueryOptions } from "../api/accounting";
import { useAccountingFormat } from "../lib/format";
import { SUPPLIER_INVOICES_ROUTE_PATH } from "../lib/routes";

/**
 * What Expenses' form shows on its supplier invoice kind once the books are
 * set up (accounting design A20, R2): supplier invoices belong in
 * Accounting's Supplier invoices, and an approved one entered in Expenses
 * waits under Needs attention to be made into one. Nothing before the books
 * are set up, or when the meta cannot be read. The host passes it to
 * `@vantigo/expenses-ui`, which imports nothing of this package.
 */
export const SupplierInvoicesNotice = () => {
  const { t } = useAccountingFormat();
  const meta = useQuery({ ...metaQueryOptions(), retry: false });
  if (!meta.data?.setUp) return null;
  return (
    <Alert color="blue" icon={<IconInfoCircle size={16} />} data-testid="supplier-invoices-notice">
      {t("supplierInvoicesInAccounting")}{" "}
      <Anchor href={appUrl(SUPPLIER_INVOICES_ROUTE_PATH)}>{t("openSupplierInvoices")}</Anchor>
    </Alert>
  );
};
