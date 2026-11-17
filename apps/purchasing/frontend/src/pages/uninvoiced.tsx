import { Anchor, Button, Group, Stack, Table, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { IconDownload } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type UninvoicedFilters, uninvoicedCsvUrl, uninvoicedQueryOptions } from "../api/purchasing";
import { PdfButton, SupplierPicker } from "../components/common";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { today, usePurchasingFormat } from "../lib/format";
import { purchaseOrderLinkOptions } from "../lib/routes";

/**
 * Received, not invoiced (purchasing design P41): per supplier and order
 * line, what is received on or before a day and not billed by a posted
 * invoice dated by then, at the order's prices — the accountant's basis for
 * a manual accrual at a period's end. Nothing is posted from it. Behind
 * purchasing:invoices-match.
 */
export const ReceivedNotInvoicedPage = () => {
  const { t, money, quantity, unitPrice, date } = usePurchasingFormat();
  const [asOf, setAsOf] = useState<string | null>(today());
  const [supplierId, setSupplierId] = useState<number | null>(null);
  const filters: UninvoicedFilters = { asOf: asOf ?? undefined, supplierId: supplierId ?? undefined };
  const list = useQuery(uninvoicedQueryOptions(filters));
  return (
    <Stack gap="lg">
      <PageHeader title={t("uninvoicedTitle")} description={t("uninvoicedDescription")} />
      <Group align="flex-end">
        <DateInput label={t("asOf")} valueFormat="YYYY-MM-DD" value={asOf} onChange={setAsOf} w={180} />
        <SupplierPicker label={t("supplier")} value={supplierId} onChange={setSupplierId} />
        {supplierId !== null && (
          <Button variant="subtle" onClick={() => setSupplierId(null)}>
            {t("allSuppliers")}
          </Button>
        )}
        <PdfButton variant="default" leftSection={<IconDownload size={16} />} url={uninvoicedCsvUrl(filters)}>
          {t("downloadCsv")}
        </PdfButton>
      </Group>
      <PageSection>
        {list.isPending ? (
          <ContentSkeleton rows={4} rowHeight={36} />
        ) : list.isError ? (
          <Text c="red" size="sm">
            {refusalMessage(list.error, t)}
          </Text>
        ) : list.data.rows.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("uninvoicedNone", { day: date(list.data.asOf) })}
          </Text>
        ) : (
          <Stack gap="sm">
            <Table.ScrollContainer minWidth={900}>
              <Table striped data-testid="uninvoiced-rows">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("supplier")}</Table.Th>
                    <Table.Th>{t("order")}</Table.Th>
                    <Table.Th>{t("product")}</Table.Th>
                    <Table.Th ta="right">{t("received")}</Table.Th>
                    <Table.Th ta="right">{t("invoiced")}</Table.Th>
                    <Table.Th ta="right">{t("notInvoiced")}</Table.Th>
                    <Table.Th ta="right">{t("orderUnitNet")}</Table.Th>
                    <Table.Th ta="right">{t("amount")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {list.data.rows.map((r) => (
                    <Table.Tr key={r.orderLineId}>
                      <Table.Td>{r.supplierName}</Table.Td>
                      <Table.Td>
                        <Anchor renderRoot={(props) => <Link {...purchaseOrderLinkOptions(r.orderId)} {...props} />}>
                          {r.orderNumber ?? r.orderId}
                        </Anchor>
                      </Table.Td>
                      <Table.Td>{r.name}</Table.Td>
                      <Table.Td ta="right">{`${quantity(r.received)} ${r.unit}`}</Table.Td>
                      <Table.Td ta="right">{quantity(r.invoiced)}</Table.Td>
                      <Table.Td ta="right">{quantity(r.quantity)}</Table.Td>
                      <Table.Td ta="right">{unitPrice(r.unitNet, r.currency)}</Table.Td>
                      <Table.Td ta="right">{money(r.amount, r.currency)}</Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
            <Group justify="space-between">
              <Text size="xs" c="dimmed">
                {t("amountsExcludeVat")}
              </Text>
              <Stack gap={0} align="flex-end" data-testid="uninvoiced-totals">
                {list.data.totals.map((total) => (
                  <Text key={total.currency} fw={600}>
                    {t("totalIn", { currency: total.currency })}: {money(total.amount, total.currency)}
                  </Text>
                ))}
              </Stack>
            </Group>
          </Stack>
        )}
      </PageSection>
    </Stack>
  );
};
