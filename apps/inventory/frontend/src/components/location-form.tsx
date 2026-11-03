import { Alert, Button, Group, Modal, NumberInput, Select, SimpleGrid, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import {
  createLocation,
  type InventoryLocation,
  type InventoryLocationKind,
  updateLocation,
  writableLocationKinds,
} from "../api/locations";
import { ApiValidationError, isRevisionConflict, refusalCode } from "../api/request";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";
import { LocationPicker } from "./location-picker";

const CODE_PATTERN = /^[A-Z0-9]{1,16}$/;

export interface LocationFormModalProps {
  opened: boolean;
  onClose: () => void;
  siteId: number;
  /** The parent a new location goes under. */
  parentId?: number;
  /** The location to edit; a new one under `parentId` when absent. */
  location?: InventoryLocation;
  /** The edited location's subtree, which cannot become its parent (I21). */
  subtree?: ReadonlySet<number>;
  onSaved?: (location: InventoryLocation) => void;
}

interface LocationFormValues {
  code: string;
  name: string;
  kind: InventoryLocationKind;
  sortOrder: number | "";
  parentId: number | null;
}

/**
 * A location's form (I2): kind, code, an optional name and a sort order; when
 * editing, the parent too — re-parenting moves the location with its subtree
 * and its stock inside its site (I21). The root takes only its name and sort
 * order: its code is the site's.
 */
export const LocationFormModal = ({
  opened,
  onClose,
  siteId,
  parentId,
  location,
  subtree,
  onSaved,
}: LocationFormModalProps) => {
  const { t } = useInventoryFormat();
  const invalidate = useInvalidateInventory();
  const [problem, setProblem] = useState<string>();
  const root = location?.kind === "site";
  const form = useForm<LocationFormValues>({
    initialValues: {
      code: location?.code ?? "",
      name: location?.name ?? "",
      kind: location?.kind ?? "shelf",
      sortOrder: location?.sortOrder ?? "",
      parentId: location?.parentId ?? parentId ?? null,
    },
    validate: {
      code: (value) => (root || CODE_PATTERN.test(value.trim().toUpperCase()) ? null : t("codeHint")),
      parentId: (value) => (root || value !== null ? null : t("required")),
    },
  });

  const save = useMutation({
    mutationFn: (values: LocationFormValues) => {
      const fields = {
        code: values.code.trim().toUpperCase(),
        kind: values.kind,
        ...(values.name.trim() ? { name: values.name.trim() } : {}),
      };
      if (location)
        return updateLocation(
          location.id,
          {
            ...fields,
            sortOrder: values.sortOrder === "" ? 0 : values.sortOrder,
            status: location.status,
            ...(root || values.parentId === null ? {} : { parentId: values.parentId }),
          },
          location.revision,
        );
      return createLocation({
        ...fields,
        parentId: values.parentId ?? 0,
        ...(values.sortOrder === "" ? {} : { sortOrder: values.sortOrder }),
      });
    },
    onSuccess: async (saved) => {
      notifications.show({
        color: "teal",
        title: t(location ? "locationSaved" : "locationCreated"),
        message: saved.fullCode,
      });
      await invalidate();
      onSaved?.(saved);
      onClose();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await invalidate();
        onClose();
        return;
      }
      const code = refusalCode(error);
      if (code === "location_code_taken" || code === "full_code_taken" || code === "code_invalid") {
        form.setFieldError("code", t(`refusal.${code}`));
        return;
      }
      if (code === "parent_invalid" || code === "depth_exceeded") {
        form.setFieldError("parentId", t(`refusal.${code}`));
        return;
      }
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
      setProblem(refusalMessage(t, error));
    },
  });

  return (
    <Modal opened={opened} onClose={onClose} title={t(location ? "editLocation" : "newLocation")} size="lg">
      <form
        onSubmit={form.onSubmit((values) => {
          setProblem(undefined);
          save.mutate(values);
        })}
      >
        <Stack gap="md">
          {!root && (
            <LocationPicker
              siteId={siteId}
              label={t("parent")}
              required
              exclude={subtree}
              value={form.values.parentId}
              onChange={(next) => form.setFieldValue("parentId", next)}
              error={form.errors.parentId as string | undefined}
            />
          )}
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            {!root && (
              <Select
                label={t("locationKind")}
                withAsterisk
                allowDeselect={false}
                data={writableLocationKinds.map((value) => ({ value, label: t(`locationKind_${value}`) }))}
                {...form.getInputProps("kind")}
              />
            )}
            {!root && (
              <TextInput
                label={t("code")}
                description={t("codeHint")}
                withAsterisk
                maxLength={16}
                {...form.getInputProps("code")}
              />
            )}
            <TextInput label={t("name")} {...form.getInputProps("name")} />
            <NumberInput label={t("sortOrder")} allowDecimal={false} {...form.getInputProps("sortOrder")} />
          </SimpleGrid>
          {problem && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
              {problem}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={save.isPending}>
              {t("save")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
