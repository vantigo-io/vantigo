import { Table } from "@mantine/core";
import type { InvoiceList } from "../api/invoices";
import "../i18n";
import { useInvoiceFormat } from "../lib/format";
import { DocumentLink } from "./document-link";
import { StateBadge } from "./state-badge";

export interface InvoiceTableProps {
  rows: InvoiceList["data"];
  /** Whether to name each document's customer — the customer panel's rows are all one customer's. */
  showCustomer: boolean;
}

/**
 * The documents as the list shows them: each linked, badged with its state
 * (D3) and, on an issued invoice, with its open amount beside its total.
 */
export const InvoiceTable = ({ rows, showCustomer }: InvoiceTableProps) => {
  const { t, money, date } = useInvoiceFormat();
  return (
    <Table.ScrollContainer minWidth={640}>
      <Table highlightOnHover>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("number")}</Table.Th>
            <Table.Th>{t("kind")}</Table.Th>
            <Table.Th>{t("state")}</Table.Th>
            {showCustomer && <Table.Th>{t("customer")}</Table.Th>}
            <Table.Th>{t("issueDate")}</Table.Th>
            <Table.Th>{t("dueDate")}</Table.Th>
            <Table.Th ta="right">{t("grossTotal")}</Table.Th>
            <Table.Th ta="right">{t("openAmount")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((row) => (
            <Table.Tr key={row.id}>
              <Table.Td>
                <DocumentLink invoiceId={row.id}>{row.number ?? t("draftNumber")}</DocumentLink>
              </Table.Td>
              <Table.Td>{row.kind === "credit_note" ? t("kindCreditNote") : t("kindInvoice")}</Table.Td>
              <Table.Td>
                <StateBadge state={row.state} />
              </Table.Td>
              {showCustomer && <Table.Td>{row.customerName ?? t("unknownCustomer")}</Table.Td>}
              <Table.Td>{row.issueDate ? date(row.issueDate) : t("notAvailable")}</Table.Td>
              <Table.Td>{row.dueDate ? date(row.dueDate) : t("notAvailable")}</Table.Td>
              <Table.Td ta="right">{money(row.grossTotal, row.currency)}</Table.Td>
              <Table.Td ta="right" data-testid="open-amount">
                {row.openAmount !== undefined ? money(row.openAmount, row.currency) : t("notAvailable")}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};
