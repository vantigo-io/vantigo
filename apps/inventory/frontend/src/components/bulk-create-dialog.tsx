import { ActionIcon, Alert, Button, Group, Modal, Select, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import {
  bulkCount,
  bulkCreateLocations,
  type InventoryBulkLevel,
  type InventoryLocationRef,
  rangeSize,
  writableLocationKinds,
} from "../api/locations";
import { refusalCode } from "../api/request";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";

/** At most this many locations a request (I5). */
const BULK_LIMIT = 1000;
/** At most this many levels a request (I5). */
const MAX_LEVELS = 3;

export interface BulkCreateDialogProps {
  opened: boolean;
  onClose: () => void;
  /** The location the grid goes under. */
  parent: Pick<InventoryLocationRef, "id" | "fullCode">;
}

const emptyLevel = (kind: InventoryBulkLevel["kind"]): InventoryBulkLevel => ({ kind, prefix: "", from: "", to: "" });

/**
 * A grid of locations under a parent in one request (I5): up to three levels,
 * each a kind, an optional prefix and a range — `01`–`12` pads to the width of
 * its start, `A`–`D` runs the letters. At most 1000, all or nothing: one
 * collision refuses the whole grid with the colliding full codes named.
 */
export const BulkCreateDialog = ({ opened, onClose, parent }: BulkCreateDialogProps) => {
  const { t } = useInventoryFormat();
  const invalidate = useInvalidateInventory();
  const [levels, setLevels] = useState<InventoryBulkLevel[]>([emptyLevel("aisle")]);
  const [problem, setProblem] = useState<string>();
  const total = bulkCount(levels);
  const invalid = levels.some((level) => rangeSize(level) === 0);

  const create = useMutation({
    mutationFn: () =>
      bulkCreateLocations({
        parentId: parent.id,
        levels: levels.map((level) => ({
          kind: level.kind,
          from: level.from.trim().toUpperCase(),
          ...(level.to?.trim() ? { to: level.to.trim().toUpperCase() } : {}),
          ...(level.prefix?.trim() ? { prefix: level.prefix.trim().toUpperCase() } : {}),
        })),
      }),
    onSuccess: async (created) => {
      notifications.show({
        color: "teal",
        title: t("bulkCreated", { count: created.length }),
        message: parent.fullCode,
      });
      await invalidate();
      setLevels([emptyLevel("aisle")]);
      onClose();
    },
    onError: (error) => {
      const code = refusalCode(error);
      // A collision names every colliding full code in its detail.
      setProblem(
        code === "location_code_taken" || code === "full_code_taken"
          ? `${t(`refusal.${code}`)} ${error.message}`
          : refusalMessage(t, error),
      );
    },
  });

  const change = (index: number, patch: Partial<InventoryBulkLevel>) =>
    setLevels(levels.map((level, i) => (i === index ? { ...level, ...patch } : level)));

  return (
    <Modal opened={opened} onClose={onClose} title={t("bulkTitle", { parent: parent.fullCode })} size="xl">
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {t("bulkHint")}
        </Text>
        {levels.map((level, index) => {
          const n = index + 1;
          return (
            <Group key={index} align="end" wrap="wrap" gap="xs">
              <Select
                w={160}
                label={t("bulkLevelKind", { n })}
                allowDeselect={false}
                data={writableLocationKinds.map((value) => ({ value, label: t(`locationKind_${value}`) }))}
                value={level.kind}
                onChange={(kind) => kind && change(index, { kind: kind as InventoryBulkLevel["kind"] })}
              />
              <TextInput
                w={110}
                label={t("bulkLevelPrefix", { n })}
                value={level.prefix ?? ""}
                onChange={(event) => change(index, { prefix: event.currentTarget.value })}
              />
              <TextInput
                w={90}
                label={t("bulkLevelFrom", { n })}
                value={level.from}
                onChange={(event) => change(index, { from: event.currentTarget.value })}
              />
              <TextInput
                w={90}
                label={t("bulkLevelTo", { n })}
                value={level.to ?? ""}
                onChange={(event) => change(index, { to: event.currentTarget.value })}
              />
              {levels.length > 1 && (
                <ActionIcon
                  variant="subtle"
                  color="red"
                  mb={4}
                  aria-label={t("bulkRemoveLevel", { n })}
                  onClick={() => setLevels(levels.filter((_, i) => i !== index))}
                >
                  <IconTrash size={16} />
                </ActionIcon>
              )}
            </Group>
          );
        })}
        {levels.length < MAX_LEVELS && (
          <Group>
            <Button
              variant="light"
              size="xs"
              leftSection={<IconPlus size={14} />}
              onClick={() => setLevels([...levels, emptyLevel("shelf")])}
            >
              {t("bulkAddLevel")}
            </Button>
          </Group>
        )}
        <Text size="sm" c={total > BULK_LIMIT ? "red" : undefined}>
          {total > BULK_LIMIT ? t("refusal.bulk_too_large") : t("bulkWillCreate", { count: total })}
        </Text>
        {problem && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {problem}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            onClick={() => {
              setProblem(undefined);
              create.mutate();
            }}
            disabled={invalid || total === 0 || total > BULK_LIMIT}
            loading={create.isPending}
          >
            {t("bulkCreate", { count: total })}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
