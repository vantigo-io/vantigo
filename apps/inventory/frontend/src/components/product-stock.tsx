import { Accordion, Alert, Anchor, Button, Card, Group, Stack, Table, Text, Title } from "@mantine/core";
import { IconAlertCircle, IconPackage } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState } from "@vantigo/frontend-shell";
import { inventoryMetaQueryOptions } from "../api/meta";
import {
  type InventoryPackaging,
  type InventorySiteStock,
  type InventoryVariantStock,
  productStockQueryOptions,
} from "../api/stock";
import { canPost } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import { newMovementLinkOptions, variantStockLinkOptions } from "../lib/routes";
import { Quantity } from "./quantity";

/** The product page's Stock card (I27), composed by the host. */
export interface ProductStockProps {
  productId: number;
}

/** The total on hand per unit: variants of one product may count in different units. */
const totalsByUnit = (variants: readonly InventoryVariantStock[]) => {
  const totals = new Map<string, number>();
  for (const one of variants) totals.set(one.variant.unit, (totals.get(one.variant.unit) ?? 0) + one.totalOnHand);
  return [...totals];
};

/** A site's line, expanding to its locations by full code and name, sorted by full code. */
const SiteStock = ({
  stock,
  unit,
  packagings,
}: {
  stock: InventorySiteStock;
  unit: string;
  packagings?: readonly InventoryPackaging[];
}) => {
  const { t } = useInventoryFormat();
  const rows = [...stock.rows].sort((a, b) => a.location.fullCode.localeCompare(b.location.fullCode));
  return (
    <Accordion.Item value={String(stock.site.id)}>
      <Accordion.Control>
        <Group justify="space-between" wrap="nowrap" pr="sm">
          <Text size="sm">
            {stock.site.code} · {stock.site.name}
          </Text>
          <Text size="sm" fw={500} ta="right" component="div">
            <Quantity value={stock.onHand} unit={unit} packagings={packagings} />
          </Text>
        </Group>
      </Accordion.Control>
      <Accordion.Panel>
        <Table verticalSpacing={4}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("columnLocation")}</Table.Th>
              <Table.Th ta="right">{t("columnOnHand")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {rows.map((row) => (
              <Table.Tr key={row.location.id}>
                <Table.Td>
                  <Text size="sm" ff="monospace" span>
                    {row.location.fullCode}
                  </Text>
                  {row.location.name && (
                    <Text size="sm" c="dimmed" span>
                      {" "}
                      {row.location.name}
                    </Text>
                  )}
                </Table.Td>
                <Table.Td ta="right">
                  <Quantity value={row.onHand} unit={unit} packagings={packagings} />
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Accordion.Panel>
    </Accordion.Item>
  );
};

/**
 * Where a product's stock lies (I27): the total on hand per unit; by site,
 * broken down in the variant's packagings where they fit (I26),
 * each expanding to its locations; per variant when the product has more
 * than one, each linking to the variant's stock and movements. With no stock
 * it says so and offers Receive or Adjust as the caller may. A Service
 * product answers no variants, and the card is not drawn.
 */
export const ProductStock = ({ productId }: ProductStockProps) => {
  const { t, quantity } = useInventoryFormat();
  const meta = useQuery(inventoryMetaQueryOptions());
  const stock = useQuery(productStockQueryOptions(productId));

  if (stock.data && stock.data.variants.length === 0) return null;

  const variants = stock.data?.variants ?? [];
  const several = variants.length > 1;
  const empty = variants.every((one) => one.totalOnHand === 0);
  const only = several ? undefined : variants[0]?.variant;

  return (
    <Card withBorder>
      <Stack gap="md">
        <Group justify="space-between">
          <Title order={4}>{t("stockCardTitle")}</Title>
          {stock.data && !empty && (
            <Text fw={600} aria-label={t("totalOnHand")}>
              {totalsByUnit(variants)
                .map(([unit, total]) => quantity(total, unit))
                .join(" · ")}
            </Text>
          )}
        </Group>
        {stock.error ? (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
            {stock.error.message}
          </Alert>
        ) : stock.isPending ? (
          <ContentSkeleton rows={2} />
        ) : empty ? (
          <EmptyState
            size="sm"
            icon={IconPackage}
            title={t("noStock")}
            description={t("noStockHint")}
            action={
              <Group gap="xs">
                {canPost(meta.data, "receipt") && (
                  <Button
                    size="xs"
                    variant="light"
                    renderRoot={(props) => (
                      <Link {...newMovementLinkOptions("receipt", { variantId: only?.id })} {...props} />
                    )}
                  >
                    {t("receive")}
                  </Button>
                )}
                {canPost(meta.data, "adjustment") && (
                  <Button
                    size="xs"
                    variant="light"
                    renderRoot={(props) => (
                      <Link {...newMovementLinkOptions("adjustment", { variantId: only?.id })} {...props} />
                    )}
                  >
                    {t("adjust")}
                  </Button>
                )}
              </Group>
            }
          />
        ) : (
          variants.map((one) => (
            <Stack key={one.variant.id} gap="xs">
              <Group justify="space-between">
                {several ? (
                  <Text fw={500} size="sm">
                    {one.variant.sku}
                    <Text span c="dimmed" size="sm">
                      {" · "}
                      {quantity(one.totalOnHand, one.variant.unit)}
                    </Text>
                  </Text>
                ) : (
                  <span />
                )}
                <Anchor
                  size="sm"
                  renderRoot={(props) => <Link {...variantStockLinkOptions(one.variant.id)} {...props} />}
                >
                  {t("variantStockLink", { sku: one.variant.sku })}
                </Anchor>
              </Group>
              {one.sites.length > 0 && (
                <Accordion multiple variant="contained" chevronPosition="left">
                  {one.sites.map((site) => (
                    <SiteStock
                      key={site.site.id}
                      stock={site}
                      unit={one.variant.unit}
                      packagings={one.variant.packagings}
                    />
                  ))}
                </Accordion>
              )}
            </Stack>
          ))
        )}
      </Stack>
    </Card>
  );
};
