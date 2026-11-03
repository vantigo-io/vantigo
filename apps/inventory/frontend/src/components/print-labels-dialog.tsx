import { Alert, Button, Checkbox, Group, Modal, MultiSelect, Radio, Stack, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { downloadBlob, type InventoryLabelLayout, labelLayouts, printLabels } from "../api/labels";
import { type InventoryLocation, siteLocationsQueryOptions } from "../api/locations";
import { locationLabel, refusalMessage, useInventoryFormat } from "../lib/format";

/** What the labels are printed for: a site, whole or some of its locations, or one location. */
export type PrintLabelsTarget =
  | { kind: "site"; siteId: number; siteCode: string; rootLocationId: number }
  | { kind: "location"; location: Pick<InventoryLocation, "id" | "fullCode"> };

export interface PrintLabelsDialogProps {
  opened: boolean;
  onClose: () => void;
  target: PrintLabelsTarget;
}

/**
 * Location labels as a PDF (I7): for a site, every location in it or the
 * ones chosen; for a location, it alone or with everything below it. Each
 * label carries the location's LOC token as a barcode, its full code and its
 * name, on an Avery L7160 or L7163 sheet. The PDF downloads.
 */
export const PrintLabelsDialog = ({ opened, onClose, target }: PrintLabelsDialogProps) => {
  const { t } = useInventoryFormat();
  const [layout, setLayout] = useState<InventoryLabelLayout>("l7160");
  const [whole, setWhole] = useState(true);
  const [chosen, setChosen] = useState<string[]>([]);
  const [below, setBelow] = useState(false);
  const site = target.kind === "site" ? target : undefined;
  const locations = useQuery({
    ...siteLocationsQueryOptions(site?.siteId ?? 0, "active"),
    enabled: !!site && !whole,
  });

  const printing = useMutation({
    mutationFn: async () => {
      const input =
        target.kind === "location"
          ? { layout, locationIds: [target.location.id], ...(below ? { subtree: true } : {}) }
          : whole
            ? { layout, locationIds: [target.rootLocationId], subtree: true }
            : { layout, locationIds: chosen.map(Number) };
      const pdf = await printLabels(input);
      const name = target.kind === "location" ? target.location.fullCode : target.siteCode;
      downloadBlob(pdf, `labels-${name}.pdf`);
    },
    onSuccess: onClose,
  });

  const nothingChosen = !!site && !whole && chosen.length === 0;

  return (
    <Modal opened={opened} onClose={onClose} title={t("printLabels")}>
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {t("printLabelsHint")}
        </Text>
        {site ? (
          <Radio.Group
            label={t("labelsFor")}
            value={whole ? "site" : "chosen"}
            onChange={(value) => setWhole(value === "site")}
          >
            <Stack gap="xs" mt="xs">
              <Radio value="site" label={t("labelsWholeSite")} />
              <Radio value="chosen" label={t("labelsChosen")} />
            </Stack>
          </Radio.Group>
        ) : (
          <Checkbox
            label={t("labelsWithBelow")}
            checked={below}
            onChange={(event) => setBelow(event.currentTarget.checked)}
          />
        )}
        {site && !whole && (
          <MultiSelect
            label={t("labelsLocations")}
            searchable
            data={(locations.data ?? []).map((location) => ({
              value: String(location.id),
              label: locationLabel(location),
            }))}
            value={chosen}
            onChange={setChosen}
            nothingFoundMessage={t("noLocationsFound")}
          />
        )}
        <Radio.Group
          label={t("labelLayout")}
          value={layout}
          onChange={(value) => setLayout(value as InventoryLabelLayout)}
        >
          <Stack gap="xs" mt="xs">
            {labelLayouts.map((value) => (
              <Radio key={value} value={value} label={t(`labelLayout_${value}`)} />
            ))}
          </Stack>
        </Radio.Group>
        {printing.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotPrint")}>
            {refusalMessage(t, printing.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button onClick={() => printing.mutate()} loading={printing.isPending} disabled={nothingChosen}>
            {t("downloadLabels")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
