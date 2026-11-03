import { Alert, Anchor, Badge, Button, Card, Group, SimpleGrid, Stack, Text } from "@mantine/core";
import {
  IconAdjustments,
  IconAlertCircle,
  IconArrowsExchange,
  IconBuildingWarehouse,
  IconPackageImport,
  IconPlus,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { inventoryMetaQueryOptions } from "../api/meta";
import { type InventorySite, sitesQueryOptions } from "../api/sites";
import { InventoryLookupField } from "../components/lookup-field";
import { SiteFormModal } from "../components/site-form";
import { canPost } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import { NEW_ADJUSTMENT_ROUTE_PATH, NEW_MOVE_ROUTE_PATH, NEW_RECEIPT_ROUTE_PATH, siteLinkOptions } from "../lib/routes";

/**
 * A site's card: its kind and address, how many locations it has, and — with
 * `stock-view` — how many stock lines lie in it and when the last receipt
 * came, all as `GET /sites` answers them on the site.
 */
const SiteCard = ({ site, canViewStock }: { site: InventorySite; canViewStock: boolean }) => {
  const { t, stamp } = useInventoryFormat();
  const city = site.address?.city;

  return (
    <Card withBorder padding="md">
      <Stack gap="xs">
        <Group justify="space-between" wrap="nowrap">
          <Anchor fw={600} renderRoot={(props) => <Link {...siteLinkOptions(site.id)} {...props} />}>
            {site.code} · {site.name}
          </Anchor>
          <Badge variant="light" color="gray">
            {t(`siteKind_${site.kind}`)}
          </Badge>
        </Group>
        {city && (
          <Text size="sm" c="dimmed">
            {city}
          </Text>
        )}
        <Text size="sm">{t("locationCount", { count: site.locationCount })}</Text>
        {canViewStock && <Text size="sm">{t("stockLineCount", { count: site.stockLines ?? 0 })}</Text>}
        {canViewStock && (
          <Text size="sm" c="dimmed">
            {site.lastReceiptAt ? t("lastReceipt", { when: stamp(site.lastReceiptAt) }) : t("noReceiptsYet")}
          </Text>
        )}
      </Stack>
    </Card>
  );
};

/**
 * The app's home (design §7): the lookup, autofocused for a scanner, and the
 * sites as cards. The header offers a new site to a manager and the three
 * postings to whoever may make them.
 */
export const InventoryHomePage = () => {
  const { t } = useInventoryFormat();
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(inventoryMetaQueryOptions());
  const canViewLocations = meta.data?.canViewLocations === true;
  const sites = useQuery({ ...sitesQueryOptions(), enabled: canViewLocations });
  const [creating, setCreating] = useState(false);

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("homeTitle")}
        description={t("homeDescription")}
        actions={
          <Group gap="xs">
            {canPost(meta.data, "receipt") && (
              <Button
                data-assist="inventory.home.new-receipt"
                variant="light"
                leftSection={<IconPackageImport size={16} />}
                renderRoot={(props) => <Link to={NEW_RECEIPT_ROUTE_PATH} {...props} />}
              >
                {t("new_receipt")}
              </Button>
            )}
            {canPost(meta.data, "adjustment") && (
              <Button
                data-assist="inventory.home.new-adjustment"
                variant="light"
                leftSection={<IconAdjustments size={16} />}
                renderRoot={(props) => <Link to={NEW_ADJUSTMENT_ROUTE_PATH} {...props} />}
              >
                {t("new_adjustment")}
              </Button>
            )}
            {canPost(meta.data, "move") && (
              <Button
                data-assist="inventory.home.new-move"
                variant="light"
                leftSection={<IconArrowsExchange size={16} />}
                renderRoot={(props) => <Link to={NEW_MOVE_ROUTE_PATH} {...props} />}
              >
                {t("new_move")}
              </Button>
            )}
            {meta.data?.canManageLocations && (
              <Button
                data-assist="inventory.home.new-site"
                leftSection={<IconPlus size={16} />}
                onClick={() => setCreating(true)}
              >
                {t("newSite")}
              </Button>
            )}
          </Group>
        }
      />
      <Card withBorder>
        <InventoryLookupField />
      </Card>
      {meta.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {meta.error.message}
        </Alert>
      ) : meta.isPending || (canViewLocations && sites.isPending) ? (
        <ContentSkeleton rows={2} rowHeight={120} />
      ) : !canViewLocations ? null : sites.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {sites.error.message}
        </Alert>
      ) : sites.data?.length === 0 ? (
        <EmptyState
          icon={IconBuildingWarehouse}
          title={t("noSites")}
          description={meta.data.canManageLocations ? t("noSitesHint") : undefined}
        />
      ) : (
        <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }}>
          {sites.data?.map((site) => (
            <SiteCard key={site.id} site={site} canViewStock={meta.data.canViewStock} />
          ))}
        </SimpleGrid>
      )}
      {creating && (
        <SiteFormModal
          opened
          onClose={() => setCreating(false)}
          onSaved={(site) => void navigate(siteLinkOptions(site.id))}
        />
      )}
    </Stack>
  );
};
