import { Alert, Button, Group, Modal, NumberInput, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { deletePlacement, type InventoryPlacement, putPlacement } from "../api/placements";
import { isRevisionConflict, NotFoundError, refusalCode } from "../api/request";
import type { InventorySiteRef } from "../api/sites";
import { QUANTITY_DECIMALS, refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";
import { roundQuantity } from "../lib/lines";
import { askToLeave } from "../lib/transfer-actions";
import { LocationPicker } from "./location-picker";

export interface PlacementDialogProps {
  opened: boolean;
  onClose: () => void;
  variant: { id: number; sku: string; unit: string };
  site: InventorySiteRef;
  /** The placement as read; a new one is created when absent. */
  placement?: InventoryPlacement;
}

type Field = "location" | "reorderPoint" | "maximum" | "multiple";

const asNumber = (value: number | string): number | "" => (value === "" ? "" : roundQuantity(Number(value)));
const orNull = (value: number | ""): number | null => (value === "" ? null : value);

/**
 * A variant's placement at one site (I54, I61): its primary location among
 * the site's active ones, its reorder point, its maximum and the multiple a
 * suggestion is rounded up to (manufacturing phase 2 M105), each with a line
 * of help; Remove deletes it. A new placement is created under
 * `If-None-Match: *`, one read is changed under `If-Match`; when it moved on
 * in between the page reloads it and says so. What the server would refuse
 * — nothing set, a maximum below the reorder point — is said before sending.
 * Needs `inventory:stock-adjust` and both views (I63).
 */
export const PlacementDialog = ({ opened, onClose, variant, site, placement }: PlacementDialogProps) => {
  const { t } = useInventoryFormat();
  const invalidate = useInvalidateInventory();
  const [locationId, setLocationId] = useState<number | null>(placement?.primaryLocationId ?? null);
  const [reorderPoint, setReorderPoint] = useState<number | "">(placement?.reorderPoint ?? "");
  const [maximum, setMaximum] = useState<number | "">(placement?.maximum ?? "");
  const [multiple, setMultiple] = useState<number | "">(placement?.multiple ?? "");
  // The revision the dialog was opened at, and whether there was a placement then: a refetch while it is open
  // must not turn the write into one against somebody else's change (I61).
  const [readAt] = useState<number | undefined>(placement?.revision);
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({});
  const [problem, setProblem] = useState<string>();
  const [dirty, setDirty] = useState(false);

  const close = async () => {
    if (
      dirty &&
      !(await askToLeave({
        title: t("leaveDraftTitle"),
        body: t("placementLeaveBody"),
        leave: t("leaveDraft"),
        stay: t("keepEditing"),
      }))
    )
      return;
    onClose();
  };

  // Somebody else changed or removed the placement since it was read: the page reads it again, and says why.
  const moved = async (error: Error) => {
    if (!isRevisionConflict(error) && !(error instanceof NotFoundError)) return false;
    notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
    await invalidate();
    onClose();
    return true;
  };
  const refused = (error: Error) => {
    const code = refusalCode(error);
    if (code === "maximum_below_reorder_point") setErrors({ maximum: t(`refusal.${code}`) });
    else if (code === "placement_location_invalid") setErrors({ location: t(`refusal.${code}`) });
    else setProblem(refusalMessage(t, error));
  };

  const saving = useMutation({
    mutationFn: () =>
      putPlacement(
        variant.id,
        site.id,
        {
          primaryLocationId: locationId,
          reorderPoint: orNull(reorderPoint),
          maximum: orNull(maximum),
          multiple: orNull(multiple),
        },
        readAt,
      ),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("placementSaved"), message: `${variant.sku} · ${site.code}` });
      await invalidate();
      onClose();
    },
    onError: async (error) => {
      if (!(await moved(error))) refused(error);
    },
  });
  const removing = useMutation({
    mutationFn: (revision: number) => deletePlacement(variant.id, site.id, revision),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("placementRemoved"), message: `${variant.sku} · ${site.code}` });
      await invalidate();
      onClose();
    },
    onError: async (error) => {
      if (!(await moved(error))) setProblem(refusalMessage(t, error));
    },
  });

  const save = () => {
    setProblem(undefined);
    const next: Partial<Record<Field, string>> = {};
    if (maximum !== "" && maximum <= 0) next.maximum = t("maximumPositive");
    else if (maximum !== "" && reorderPoint !== "" && maximum < reorderPoint)
      next.maximum = t("refusal.maximum_below_reorder_point");
    if (multiple !== "" && multiple <= 0) next.multiple = t("multiplePositive");
    setErrors(next);
    if (Object.keys(next).length > 0) return;
    // A placement with nothing set is never stored (placement_empty): an existing one is removed instead.
    if (locationId === null && reorderPoint === "" && maximum === "") {
      setProblem(t("refusal.placement_empty"));
      return;
    }
    saving.mutate();
  };

  const edit =
    <T,>(set: (value: T) => void, field: Field) =>
    (value: T) => {
      set(value);
      setDirty(true);
      setErrors((current) => ({ ...current, [field]: undefined }));
    };

  return (
    <Modal opened={opened} onClose={() => void close()} title={t("placementTitle", { site: site.name })}>
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {variant.sku} · {site.code}
        </Text>
        <LocationPicker
          siteId={site.id}
          label={t("primaryLocation")}
          description={t("primaryLocationHelp")}
          value={locationId}
          onChange={edit(setLocationId, "location")}
          error={errors.location}
        />
        <NumberInput
          label={t("reorderPoint")}
          description={t("reorderPointHelp")}
          decimalScale={QUANTITY_DECIMALS}
          allowNegative={false}
          min={0}
          rightSection={
            <Text size="xs" c="dimmed" pr="xs">
              {variant.unit}
            </Text>
          }
          rightSectionWidth={48}
          value={reorderPoint}
          onChange={(value) => edit(setReorderPoint, "reorderPoint")(asNumber(value))}
          error={errors.reorderPoint}
        />
        <NumberInput
          label={t("maximumLevel")}
          description={t("maximumHelp")}
          decimalScale={QUANTITY_DECIMALS}
          allowNegative={false}
          min={0}
          rightSection={
            <Text size="xs" c="dimmed" pr="xs">
              {variant.unit}
            </Text>
          }
          rightSectionWidth={48}
          value={maximum}
          onChange={(value) => edit(setMaximum, "maximum")(asNumber(value))}
          error={errors.maximum}
        />
        <NumberInput
          label={t("multiple")}
          description={t("multipleHelp")}
          decimalScale={QUANTITY_DECIMALS}
          allowNegative={false}
          min={0}
          rightSection={
            <Text size="xs" c="dimmed" pr="xs">
              {variant.unit}
            </Text>
          }
          rightSectionWidth={48}
          value={multiple}
          onChange={(value) => edit(setMultiple, "multiple")(asNumber(value))}
          error={errors.multiple}
          data-assist="inventory.placement.multiple"
        />
        {problem && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {problem}
          </Alert>
        )}
        <Group justify="space-between">
          {readAt !== undefined ? (
            <Button color="red" variant="subtle" loading={removing.isPending} onClick={() => removing.mutate(readAt)}>
              {t("removePlacement")}
            </Button>
          ) : (
            <span />
          )}
          <Group gap="xs">
            <Button variant="default" onClick={() => void close()}>
              {t("cancel")}
            </Button>
            <Button onClick={save} loading={saving.isPending}>
              {t("save")}
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  );
};
