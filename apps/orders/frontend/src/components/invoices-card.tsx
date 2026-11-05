import { Anchor, Badge, Card, Stack, Table, Text, Title } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import type { OrderInvoice } from "../api/types";
import { hasWords } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { invoiceLinkOptions } from "../lib/routes";

const stateColours: Record<string, string> = { invoiced: "teal", released: "gray" };

export interface InvoicesCardProps {
  invoices: readonly OrderInvoice[];
  /** Whether the caller can open an invoice in Invoices (`invoices:access`); otherwise the numbers are plain words. */
  canViewInvoices: boolean;
}

/**
 * The invoices issued from an order (orders design O41), from Orders' own
 * allocations: each one's number — linking to Invoices when the caller can
 * open it — its date, the part of the order it still bills, net of VAT,
 * what credit notes gave back of it, and whether they gave all of it back.
 */
export const InvoicesCard = ({ invoices, canViewInvoices }: InvoicesCardProps) => {
  const { t, money, date } = useOrdersFormat();
  return (
    <Card withBorder padding="lg" data-testid="invoices-card">
      <Stack gap="md">
        <Title order={4}>{t("orderInvoices")}</Title>
        {invoices.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noOrderInvoices")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={420}>
            <Table verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("invoiceNumber")}</Table.Th>
                  <Table.Th>{t("invoiceDate")}</Table.Th>
                  <Table.Th ta="right">{t("netAmount")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {invoices.map((invoice) => (
                  <Table.Tr key={invoice.invoiceId}>
                    <Table.Td>
                      {canViewInvoices ? (
                        <Anchor
                          size="sm"
                          renderRoot={(props) => <Link {...invoiceLinkOptions(invoice.invoiceId)} {...props} />}
                        >
                          {invoice.invoiceNumber}
                        </Anchor>
                      ) : (
                        <Text size="sm">{invoice.invoiceNumber}</Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm">{date(invoice.issueDate)}</Text>
                    </Table.Td>
                    <Table.Td ta="right">
                      <Text size="sm" ff="monospace">
                        {money(invoice.amount, invoice.currency)}
                      </Text>
                      {invoice.releasedAmount > 0 && (
                        <Text size="xs" c="dimmed" ff="monospace">
                          {t("creditedAmount", { amount: money(invoice.releasedAmount, invoice.currency) })}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="light" color={stateColours[invoice.state] ?? "gray"}>
                        {hasWords(`orderInvoiceState.${invoice.state}`)
                          ? t(`orderInvoiceState.${invoice.state}`)
                          : invoice.state}
                      </Badge>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
    </Card>
  );
};
