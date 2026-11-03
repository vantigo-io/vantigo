import { Alert, Badge, Button, Card, Group, Stack, Text, Title } from "@mantine/core";
import { IconAdjustments, IconAlertCircle, IconPackage, IconPackageImport } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { inventoryMetaQueryOptions } from "../api/meta";
import { variantStockQueryOptions } from "../api/stock";
import { MovementList } from "../components/movement-list";
import { StockTable } from "../components/stock-table";
import { canPost } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import { INVENTORY_HOME_ROUTE_PATH, newMovementLinkOptions } from "../lib/routes";

/**
 * One variant's stock (design §7): the total, each site with its locations
 * by full code, and the variant's movements, newest first. A variant the
 * catalog no longer knows is shown from its last movement's snapshot.
 */
export const VariantStockPage = () => {
  const { t, quantity } = useInventoryFormat();
  const { variantId: rawId } = useParams({ strict: false }) as { variantId: string };
  const variantId = Number(rawId);
  const meta = useQuery(inventoryMetaQueryOptions());
  const stock = useQuery(variantStockQueryOptions(variantId));
  const variant = stock.data?.variant;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH }, { label: variant?.sku ?? "…" }]}
        title={
          <Group gap="sm">
            <span>{variant ? variant.sku : t("variantById", { id: variantId })}</span>
            {variant?.deleted && (
              <Badge variant="light" color="gray">
                {t("variantDeleted")}
              </Badge>
            )}
          </Group>
        }
        description={variant?.productName}
        actions={
          variant &&
          !variant.deleted && (
            <Group gap="xs">
              {canPost(meta.data, "receipt") && (
                <Button
                  variant="light"
                  leftSection={<IconPackageImport size={16} />}
                  renderRoot={(props) => <Link {...newMovementLinkOptions("receipt", { variantId })} {...props} />}
                >
                  {t("receive")}
                </Button>
              )}
              {canPost(meta.data, "adjustment") && (
                <Button
                  variant="light"
                  leftSection={<IconAdjustments size={16} />}
                  renderRoot={(props) => <Link {...newMovementLinkOptions("adjustment", { variantId })} {...props} />}
                >
                  {t("adjust")}
                </Button>
              )}
            </Group>
          )
        }
      />
      {stock.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {stock.error.message}
        </Alert>
      ) : stock.isPending ? (
        <ContentSkeleton rows={4} />
      ) : (
        <Card withBorder>
          <Stack gap="md">
            <Group justify="space-between">
              <Title order={4}>{t("stockCardTitle")}</Title>
              <Text fw={600}>{quantity(stock.data.totalOnHand, stock.data.variant.unit)}</Text>
            </Group>
            {stock.data.sites.length === 0 ? (
              <EmptyState size="sm" icon={IconPackage} title={t("noStock")} />
            ) : (
              stock.data.sites.map((site) => (
                <Stack key={site.site.id} gap={4}>
                  <Group justify="space-between">
                    <Text fw={500} size="sm">
                      {site.site.code} · {site.site.name}
                    </Text>
                    <Text size="sm">{quantity(site.onHand, stock.data.variant.unit)}</Text>
                  </Group>
                  <StockTable rows={site.rows} hideVariant />
                </Stack>
              ))
            )}
          </Stack>
        </Card>
      )}
      <Card withBorder>
        <Stack gap="sm">
          <Title order={4}>{t("navMovements")}</Title>
          <MovementList filter={{ variantId }} limit={20} />
        </Stack>
      </Card>
    </Stack>
  );
};
