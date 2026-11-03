import { Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  deleteLocation,
  type InventoryLocation,
  type InventoryLocationRef,
  locationUpdateOf,
  updateLocation,
} from "../api/locations";
import { isRevisionConflict } from "../api/request";
import { deleteSite, type InventorySite, siteUpdateOf, updateSite } from "../api/sites";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "./format";

/** A location's or a site's descendants by id, from the site's flat list: what cannot become its parent. */
export const subtreeOf = (locations: readonly Pick<InventoryLocation, "id" | "parentId">[], id: number) => {
  const below = new Set<number>([id]);
  let grew = true;
  while (grew) {
    grew = false;
    for (const location of locations)
      if (location.parentId !== undefined && below.has(location.parentId) && !below.has(location.id)) {
        below.add(location.id);
        grew = true;
      }
  }
  return below;
};

/**
 * Archive, reactivate and delete for locations and sites (I6), each behind a
 * confirmation where it cannot be taken back lightly. A refusal —
 * `location_has_stock` on archive, `location_has_history` on delete — is said
 * in the module's words.
 */
export const useStructureActions = () => {
  const { t } = useInventoryFormat();
  const invalidate = useInvalidateInventory();

  const failed = async (title: string, error: Error) => {
    if (isRevisionConflict(error)) {
      notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
      await invalidate();
      return;
    }
    notifications.show({ color: "red", title, message: refusalMessage(t, error) });
  };

  const run = async (work: () => Promise<unknown>, done: string, failure: string, after?: () => void) => {
    try {
      await work();
      notifications.show({ color: "teal", title: done, message: "" });
      await invalidate();
      after?.();
    } catch (error) {
      await failed(failure, error as Error);
    }
  };

  const confirm = (title: string, body: string, label: string, onConfirm: () => void) =>
    modals.openConfirmModal({
      title,
      children: <Text size="sm">{body}</Text>,
      labels: { confirm: label, cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm,
    });

  return {
    archiveLocation: (location: InventoryLocation) =>
      confirm(
        t("archiveLocationTitle", { code: location.fullCode }),
        t("archiveLocationBody"),
        t("archive"),
        () =>
          void run(
            () => updateLocation(location.id, { ...locationUpdateOf(location), status: "archived" }, location.revision),
            t("locationArchived"),
            t("couldNotArchive"),
          ),
      ),
    reactivateLocation: (location: InventoryLocation) =>
      void run(
        () => updateLocation(location.id, { ...locationUpdateOf(location), status: "active" }, location.revision),
        t("locationReactivated"),
        t("couldNotSave"),
      ),
    deleteLocation: (location: InventoryLocationRef, after?: () => void) =>
      confirm(
        t("deleteLocationTitle", { code: location.fullCode }),
        t("deleteLocationBody"),
        t("delete"),
        () => void run(() => deleteLocation(location.id), t("locationDeleted"), t("couldNotDelete"), after),
      ),
    archiveSite: (site: InventorySite) =>
      confirm(
        t("archiveSiteTitle", { code: site.code }),
        t("archiveSiteBody"),
        t("archive"),
        () =>
          void run(
            () => updateSite(site.id, { ...siteUpdateOf(site), status: "archived" }, site.revision),
            t("siteArchived"),
            t("couldNotArchive"),
          ),
      ),
    reactivateSite: (site: InventorySite) =>
      void run(
        () => updateSite(site.id, { ...siteUpdateOf(site), status: "active" }, site.revision),
        t("siteReactivated"),
        t("couldNotSave"),
      ),
    deleteSite: (site: InventorySite, after?: () => void) =>
      confirm(
        t("deleteSiteTitle", { code: site.code }),
        t("deleteSiteBody"),
        t("delete"),
        () => void run(() => deleteSite(site.id), t("siteDeleted"), t("couldNotDelete"), after),
      ),
  };
};
