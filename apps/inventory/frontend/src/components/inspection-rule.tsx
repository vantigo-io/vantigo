import { Stack, Switch, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery } from "@tanstack/react-query";
import { inventoryMetaQueryOptions } from "../api/meta";
import { putVariantRules, variantRulesQueryOptions } from "../api/rules";
import type { InventoryVariantRef } from "../api/stock";
import { canPost } from "../lib/capabilities";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";

/**
 * Inspect on receipt on the variant's page (manufacturing phase 2 M100):
 * every lot a receipt of the variant makes or adds to is put in quarantine
 * for inspection, released on the lot page. Only a tracked variant has it;
 * setting it needs `inventory:stock-adjust`.
 */
export const InspectionRule = ({ variant }: { variant: InventoryVariantRef }) => {
  const { t } = useInventoryFormat();
  const meta = useQuery(inventoryMetaQueryOptions());
  const rules = useQuery(variantRulesQueryOptions(variant.id));
  const invalidate = useInvalidateInventory();
  const tracked = variant.tracking === "lot" || variant.tracking === "serial";
  const save = useMutation({
    mutationFn: (next: boolean) => putVariantRules(variant.id, next, rules.data?.revision ?? 0),
    onSuccess: async (saved) => {
      notifications.show({
        color: "teal",
        title: t(saved.inspectOnReceipt ? "inspectionOn" : "inspectionOff"),
        message: variant.sku,
      });
      await invalidate();
    },
    onError: async (error) => {
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(t, error as Error) });
      await invalidate();
    },
  });
  if (!tracked && !rules.data?.inspectOnReceipt) return null;
  return (
    <Stack gap={4} data-assist="inventory.variant.inspection">
      <Switch
        label={t("inspectOnReceipt")}
        checked={rules.data?.inspectOnReceipt ?? false}
        disabled={!canPost(meta.data, "adjustment") || rules.isPending || save.isPending || !tracked}
        onChange={(event) => save.mutate(event.currentTarget.checked)}
      />
      <Text size="xs" c="dimmed">
        {t("inspectOnReceiptHint")}
      </Text>
    </Stack>
  );
};
