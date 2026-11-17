import { Alert, Anchor, Badge, Card, Group, Stack, Table, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { supplierItemsQueryOptions } from "../api/purchasing";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { usePurchasingFormat } from "../lib/format";
import { supplierLinkOptions } from "../lib/routes";

export interface ProductSuppliersCardProps {
  /** The product's variants, whose supplier items the card lists (at most 200). */
  variantIds: readonly number[];
}

/**
 * The product page's **Suppliers** card (P13), composed by the host for a
 * holder of purchasing:access while Purchasing is enabled: every supplier
 * item of the product's variants — who sells it, under what number, at what
 * price and lead time, the preferred one marked — each linking to the
 * supplier's page in Purchasing, where the items are written.
 */
export const ProductSuppliersCard = ({ variantIds }: ProductSuppliersCardProps) => {
  const { t, unitPrice } = usePurchasingFormat();
  const ids = variantIds.slice(0, 200);
  const items = useQuery({ ...supplierItemsQueryOptions({ variantIds: ids, pageSize: 200 }), enabled: ids.length > 0 });
  return (
    <Card withBorder padding="md" data-testid="product-suppliers">
      <Stack gap="sm">
        <Title order={4}>{t("productSuppliersTitle")}</Title>
        {ids.length === 0 || items.data?.items.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("productSuppliersEmpty")}
          </Text>
        ) : items.isError ? (
          <Alert color="red" title={t("couldNotLoad")}>
            {refusalMessage(items.error, t)}
          </Alert>
        ) : items.isPending ? (
          <ContentSkeleton rows={2} />
        ) : (
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("supplier")}</Table.Th>
                <Table.Th>{t("sku")}</Table.Th>
                <Table.Th>{t("supplierItemNumber")}</Table.Th>
                <Table.Th ta="right">{t("price")}</Table.Th>
                <Table.Th>{t("leadTime")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {items.data.items.map((item) => (
                <Table.Tr key={item.id}>
                  <Table.Td>
                    <Group gap={6} wrap="nowrap">
                      <Anchor renderRoot={(props) => <Link {...supplierLinkOptions(item.supplierId)} {...props} />}>
                        {item.supplierName}
                      </Anchor>
                      {item.preferred && (
                        <Badge size="xs" variant="light">
                          {t("preferred")}
                        </Badge>
                      )}
                    </Group>
                  </Table.Td>
                  <Table.Td>{item.sku}</Table.Td>
                  <Table.Td>{item.supplierItemNumber ?? ""}</Table.Td>
                  <Table.Td ta="right">
                    {item.price !== null && item.price !== undefined ? unitPrice(item.price, item.currency) : "—"}
                    {item.packagingName ? ` / ${item.packagingName}` : ""}
                  </Table.Td>
                  <Table.Td>
                    {item.leadTimeDays !== null && item.leadTimeDays !== undefined
                      ? t("leadTimeValue", { count: item.leadTimeDays })
                      : ""}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
    </Card>
  );
};
