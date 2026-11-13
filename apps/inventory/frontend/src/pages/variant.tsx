import { Alert, Badge, Button, Group, Stack, Text, Title } from "@mantine/core";
import { IconAdjustments, IconAlertCircle, IconPackage, IconPackageImport, IconPencil } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { inventoryMetaQueryOptions } from "../api/meta";
import type { InventoryPlacement } from "../api/placements";
import { type InventorySiteRef, sitesQueryOptions } from "../api/sites";
import { type InventoryVariantRef, variantStockQueryOptions } from "../api/stock";
import { InspectionRule } from "../components/inspection-rule";
import { MovementList } from "../components/movement-list";
import { PlacementDialog } from "../components/placement-dialog";
import { HeldQuantity, ReservedAvailable } from "../components/product-stock";
import { StockTable } from "../components/stock-table";
import { TransferList } from "../components/transfer-list";
import { canPost, canViewTransfers } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import { INVENTORY_HOME_ROUTE_PATH, newMovementLinkOptions } from "../lib/routes";

/**
 * A site's placement on the variant's page (I54, I56): the reorder point and
 * the maximum when set, *Low* while available plus incoming is at or below the
 * reorder point, and Edit placement for a holder of `inventory:stock-adjust`.
 */
const SitePlacement = ({
  variant,
  placement,
  onEdit,
}: {
  variant: InventoryVariantRef;
  placement?: InventoryPlacement;
  /** Opens the placement dialog; absent for a caller who cannot adjust stock. */
  onEdit?: () => void;
}) => {
  const { t, quantity } = useInventoryFormat();
  const canEdit = onEdit !== undefined;
  const levels = [
    ...(placement?.reorderPoint !== null && placement?.reorderPoint !== undefined
      ? [`${t("reorderPoint")} ${quantity(placement.reorderPoint, variant.unit)}`]
      : []),
    ...(placement?.maximum !== null && placement?.maximum !== undefined
      ? [`${t("maximumLevel")} ${quantity(placement.maximum, variant.unit)}`]
      : []),
    ...(placement?.multiple !== null && placement?.multiple !== undefined
      ? [`${t("multiple")} ${quantity(placement.multiple, variant.unit)}`]
      : []),
  ];
  if (levels.length === 0 && !canEdit) return null;
  return (
    <Group gap="xs">
      {levels.length > 0 && (
        <Text size="xs" c="dimmed">
          {levels.join(" · ")}
        </Text>
      )}
      {placement?.low && (
        <Badge size="xs" variant="light" color="red">
          {t("low")}
        </Badge>
      )}
      {canEdit && !variant.deleted && (
        <Button size="compact-xs" variant="subtle" leftSection={<IconPencil size={12} />} onClick={onEdit}>
          {t("editPlacement")}
        </Button>
      )}
    </Group>
  );
};

/**
 * One variant's stock (design §7): the total, what orders have reserved and
 * what is available (orders design O45), each site with its locations
 * by full code — its primary location first, marked — with the site's
 * placement (I54), and the variant's movements, newest first. A holder of
 * `inventory:stock-adjust` sets a placement at any active site, one that holds
 * none of it too. A variant the
 * catalog no longer knows is shown from its last movement's snapshot.
 */
export const VariantStockPage = () => {
  const { t, quantity } = useInventoryFormat();
  const { variantId: rawId } = useParams({ strict: false }) as { variantId: string };
  const variantId = Number(rawId);
  const meta = useQuery(inventoryMetaQueryOptions());
  const stock = useQuery(variantStockQueryOptions(variantId));
  const variant = stock.data?.variant;
  const canPlace = canPost(meta.data, "adjustment");
  const sites = useQuery({ ...sitesQueryOptions(), enabled: canPlace });
  // The active sites holding none of it, so a placement can be set there before the first receipt.
  const elsewhere = (sites.data ?? []).filter((one) => !stock.data?.sites.some((held) => held.site.id === one.id));
  // The site whose placement is being edited, kept here so a refetch moving the site between the lists below
  // does not close the dialog mid-edit; the dialog keeps the placement as it was when it opened.
  const [editing, setEditing] = useState<{ site: InventorySiteRef; placement?: InventoryPlacement }>();

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
        <PageSection>
          <Stack gap="md">
            <Group justify="space-between">
              <Title order={4}>{t("stockCardTitle")}</Title>
              <Stack gap={0} align="flex-end">
                <Text fw={600}>{quantity(stock.data.totalOnHand, stock.data.variant.unit)}</Text>
                <ReservedAvailable
                  reserved={stock.data.totalReserved}
                  available={stock.data.totalAvailable}
                  unit={stock.data.variant.unit}
                />
              </Stack>
            </Group>
            {!stock.data.variant.deleted && <InspectionRule variant={stock.data.variant} />}
            {stock.data.sites.some((site) => site.available < 0) && (
              <Alert color="red" variant="light" icon={<IconAlertCircle size={16} />}>
                {t("availableNegative")}
              </Alert>
            )}
            {stock.data.sites.length === 0 ? (
              <EmptyState size="sm" icon={IconPackage} title={t("noStock")} />
            ) : (
              stock.data.sites.map((site) => (
                <Stack key={site.site.id} gap={4}>
                  <Group justify="space-between" align="flex-start">
                    <Stack gap={2}>
                      <Text fw={500} size="sm">
                        {site.site.code} · {site.site.name}
                      </Text>
                      <SitePlacement
                        variant={stock.data.variant}
                        placement={site.placement}
                        onEdit={canPlace ? () => setEditing({ site: site.site, placement: site.placement }) : undefined}
                      />
                    </Stack>
                    <Stack gap={0} align="flex-end">
                      <Text size="sm">{quantity(site.onHand, stock.data.variant.unit)}</Text>
                      {site.incoming > 0 && (
                        <Text size="xs" c="blue">
                          {t("incomingQuantity", { quantity: quantity(site.incoming, stock.data.variant.unit) })}
                        </Text>
                      )}
                      <ReservedAvailable
                        reserved={site.reserved}
                        available={site.available}
                        unit={stock.data.variant.unit}
                      />
                      <HeldQuantity held={site.held} unit={stock.data.variant.unit} />
                    </Stack>
                  </Group>
                  {site.rows.length > 0 && <StockTable rows={site.rows} hideVariant />}
                </Stack>
              ))
            )}
            {canPlace &&
              elsewhere.map((site) => (
                <Group key={site.id} justify="space-between">
                  <Stack gap={2}>
                    <Text fw={500} size="sm" c="dimmed">
                      {site.code} · {site.name}
                    </Text>
                    <SitePlacement variant={stock.data.variant} onEdit={() => setEditing({ site })} />
                  </Stack>
                  <Text size="sm" c="dimmed">
                    —
                  </Text>
                </Group>
              ))}
            {editing && (
              <PlacementDialog
                key={editing.site.id}
                opened
                onClose={() => setEditing(undefined)}
                variant={stock.data.variant}
                site={editing.site}
                placement={editing.placement}
              />
            )}
          </Stack>
        </PageSection>
      )}
      <PageSection>
        <Stack gap="sm">
          <Title order={4}>{t("navMovements")}</Title>
          <MovementList filter={{ variantId }} limit={20} />
        </Stack>
      </PageSection>
      {canViewTransfers(meta.data) && (
        <PageSection>
          <Stack gap="sm">
            <Title order={4}>{t("transferDifferences")}</Title>
            <Text size="sm" c="dimmed">
              {t("transferDifferencesHint")}
            </Text>
            <TransferList
              filter={{ variantId, withMissing: true }}
              showMissing
              emptyTitle={t("noTransferDifferences")}
            />
          </Stack>
        </PageSection>
      )}
    </Stack>
  );
};
