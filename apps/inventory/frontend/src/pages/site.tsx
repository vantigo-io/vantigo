import { ActionIcon, Alert, Badge, Button, Card, Group, Menu, Stack, Switch, Text } from "@mantine/core";
import {
  IconAlertCircle,
  IconAlertTriangle,
  IconArchive,
  IconClipboardList,
  IconDots,
  IconGridDots,
  IconPencil,
  IconPlus,
  IconPrinter,
  IconRestore,
  IconTrash,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type InventoryLocation, siteLocationListQueryOptions } from "../api/locations";
import { inventoryMetaQueryOptions } from "../api/meta";
import { type InventorySite, siteQueryOptions } from "../api/sites";
import { BulkCreateDialog } from "../components/bulk-create-dialog";
import { LocationFormModal } from "../components/location-form";
import { LIVE_SEARCH_LIMIT, LocationTree } from "../components/location-tree";
import { OpenCountDialog } from "../components/open-count-dialog";
import { PrintLabelsDialog } from "../components/print-labels-dialog";
import { SiteFormModal } from "../components/site-form";
import { subtreeOf, useStructureActions } from "../lib/actions";
import { canCount, canPrintLabels } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import { INVENTORY_HOME_ROUTE_PATH, movementsLinkOptions } from "../lib/routes";

type Dialog =
  | { type: "new"; parent: InventoryLocation }
  | { type: "bulk"; parent: InventoryLocation }
  | { type: "edit"; location: InventoryLocation }
  | { type: "site" }
  | { type: "labels" }
  | { type: "count" };

/** A site's address on one line. */
const addressLine = (site: InventorySite) =>
  [
    site.address?.line1,
    site.address?.line2,
    [site.address?.postalCode, site.address?.city].filter(Boolean).join(" "),
    site.address?.country,
  ]
    .filter(Boolean)
    .join(", ");

/**
 * A site (design §7): its header and its location tree, expanding a level at
 * a time and searchable by code. A manager (`locations-manage`) adds a
 * location or a grid of them, edits, archives and deletes — the site's own
 * actions in the header, each location's in its row's menu.
 */
export const SitePage = () => {
  const { t } = useInventoryFormat();
  const { siteId: rawId } = useParams({ strict: false }) as { siteId: string };
  const siteId = Number(rawId);
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(inventoryMetaQueryOptions());
  const site = useQuery(siteQueryOptions(siteId));
  const list = useQuery(siteLocationListQueryOptions(siteId));
  const locations = list.data?.locations;
  const actions = useStructureActions();
  const [dialog, setDialog] = useState<Dialog>();
  const [showArchived, setShowArchived] = useState(false);
  const canManage = meta.data?.canManageLocations === true;
  const root = locations?.find((location) => location.id === site.data?.rootLocationId);
  const shown = (locations ?? []).filter((location) => showArchived || location.status === "active");
  const failed = site.error ?? list.error;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH }, { label: site.data?.code ?? "…" }]}
        title={
          <Group gap="sm">
            <span>{site.data ? `${site.data.code} · ${site.data.name}` : t("siteById", { id: siteId })}</span>
            {site.data && (
              <Badge variant="light" color="gray">
                {t(`siteKind_${site.data.kind}`)}
              </Badge>
            )}
            {site.data?.status === "archived" && (
              <Badge variant="light" color="yellow">
                {t("status_archived")}
              </Badge>
            )}
          </Group>
        }
        description={site.data && addressLine(site.data)}
        actions={
          site.data && (
            <Group gap="xs">
              <Button
                variant="default"
                renderRoot={(props) => <Link {...movementsLinkOptions({ siteId })} {...props} />}
              >
                {t("navMovements")}
              </Button>
              {canPrintLabels(meta.data) && (
                <Button
                  data-assist="inventory.site.print-labels"
                  variant="default"
                  leftSection={<IconPrinter size={16} />}
                  onClick={() => setDialog({ type: "labels" })}
                >
                  {t("printLabels")}
                </Button>
              )}
              {canCount(meta.data) && site.data.status === "active" && (
                <Button
                  variant="light"
                  leftSection={<IconClipboardList size={16} />}
                  onClick={() => setDialog({ type: "count" })}
                >
                  {t("countHere")}
                </Button>
              )}
              {canManage && root && site.data.status === "active" && (
                <Button
                  data-assist="inventory.site.new-location"
                  leftSection={<IconPlus size={16} />}
                  onClick={() => setDialog({ type: "new", parent: root })}
                >
                  {t("newLocation")}
                </Button>
              )}
              {canManage && root && site.data.status === "active" && (
                <Button
                  data-assist="inventory.site.bulk-add"
                  variant="light"
                  leftSection={<IconGridDots size={16} />}
                  onClick={() => setDialog({ type: "bulk", parent: root })}
                >
                  {t("bulkAdd")}
                </Button>
              )}
              {canManage && (
                <Menu position="bottom-end">
                  <Menu.Target>
                    <ActionIcon variant="default" size="lg" aria-label={t("siteActions")}>
                      <IconDots size={16} />
                    </ActionIcon>
                  </Menu.Target>
                  <Menu.Dropdown>
                    <Menu.Item leftSection={<IconPencil size={14} />} onClick={() => setDialog({ type: "site" })}>
                      {t("editSite")}
                    </Menu.Item>
                    {site.data.status === "active" ? (
                      <Menu.Item
                        leftSection={<IconArchive size={14} />}
                        onClick={() => site.data && actions.archiveSite(site.data)}
                      >
                        {t("archive")}
                      </Menu.Item>
                    ) : (
                      <Menu.Item
                        leftSection={<IconRestore size={14} />}
                        onClick={() => site.data && actions.reactivateSite(site.data)}
                      >
                        {t("reactivate")}
                      </Menu.Item>
                    )}
                    <Menu.Item
                      color="red"
                      leftSection={<IconTrash size={14} />}
                      onClick={() =>
                        site.data &&
                        actions.deleteSite(site.data, () => void navigate({ to: INVENTORY_HOME_ROUTE_PATH }))
                      }
                    >
                      {t("delete")}
                    </Menu.Item>
                  </Menu.Dropdown>
                </Menu>
              )}
            </Group>
          )
        }
      />
      {failed ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {failed.message}
        </Alert>
      ) : site.isPending || list.isPending ? (
        <ContentSkeleton rows={6} rowHeight={32} />
      ) : (
        <Card withBorder>
          <Stack gap="sm">
            <Group justify="space-between">
              <Text fw={500}>{t("locations")}</Text>
              <Switch
                label={t("showArchived")}
                checked={showArchived}
                onChange={(event) => setShowArchived(event.currentTarget.checked)}
              />
            </Group>
            {list.data?.truncated && (
              <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
                {t("locationsTruncated")}
              </Alert>
            )}
            <LocationTree
              locations={shown}
              serverSearch={
                list.data?.truncated || (site.data?.locationCount ?? 0) > LIVE_SEARCH_LIMIT
                  ? { siteId, status: showArchived ? undefined : "active" }
                  : undefined
              }
              actions={
                canManage
                  ? {
                      onAddChild: (parent) => setDialog({ type: "new", parent }),
                      onBulkAdd: (parent) => setDialog({ type: "bulk", parent }),
                      onEdit: (location) => setDialog({ type: "edit", location }),
                      onArchive: actions.archiveLocation,
                      onReactivate: actions.reactivateLocation,
                      onDelete: (location) => actions.deleteLocation(location),
                    }
                  : undefined
              }
            />
            <Text size="xs" c="dimmed">
              {t("locationCount", { count: site.data?.locationCount ?? 0 })}
            </Text>
          </Stack>
        </Card>
      )}
      {dialog?.type === "new" && (
        <LocationFormModal opened onClose={() => setDialog(undefined)} siteId={siteId} parentId={dialog.parent.id} />
      )}
      {dialog?.type === "bulk" && (
        <BulkCreateDialog opened onClose={() => setDialog(undefined)} parent={dialog.parent} />
      )}
      {dialog?.type === "edit" && (
        <LocationFormModal
          opened
          onClose={() => setDialog(undefined)}
          siteId={siteId}
          location={dialog.location}
          subtree={subtreeOf(locations ?? [], dialog.location.id)}
        />
      )}
      {dialog?.type === "labels" && site.data && (
        <PrintLabelsDialog
          opened
          onClose={() => setDialog(undefined)}
          target={{
            kind: "site",
            siteId,
            siteCode: site.data.code,
            rootLocationId: site.data.rootLocationId,
          }}
        />
      )}
      {dialog?.type === "count" && <OpenCountDialog opened onClose={() => setDialog(undefined)} siteId={siteId} />}
      {dialog?.type === "site" && site.data && (
        <SiteFormModal opened onClose={() => setDialog(undefined)} site={site.data} />
      )}
    </Stack>
  );
};
