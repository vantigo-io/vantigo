import { Alert, Button, Group, Modal, Select, SimpleGrid, Stack, Switch, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { ApiValidationError, isRevisionConflict, refusalCode } from "../api/request";
import {
  createSite,
  type InventorySite,
  type InventorySiteInput,
  type InventorySiteKind,
  siteKinds,
  updateSite,
} from "../api/sites";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";

/** A code as the server keeps it: 1–16 characters A–Z and 0–9, upper-cased (I1). */
const CODE_PATTERN = /^[A-Z0-9]{1,16}$/;

export interface SiteFormModalProps {
  opened: boolean;
  onClose: () => void;
  /** The site to edit; a new site when absent. */
  site?: InventorySite;
  onSaved?: (site: InventorySite) => void;
}

interface SiteFormValues {
  code: string;
  name: string;
  kind: InventorySiteKind;
  fulfilsOrders: boolean;
  line1: string;
  line2: string;
  postalCode: string;
  city: string;
  country: string;
}

/**
 * A site's form (I1): code, name, kind, whether it fulfils orders and an
 * optional address. Creating one
 * creates its root location with it; a new code rewrites every full code in
 * the site (I4). The status is changed by Archive and Reactivate, not here.
 */
export const SiteFormModal = ({ opened, onClose, site, onSaved }: SiteFormModalProps) => {
  const { t } = useInventoryFormat();
  const invalidate = useInvalidateInventory();
  const [problem, setProblem] = useState<string>();
  const form = useForm<SiteFormValues>({
    initialValues: {
      code: site?.code ?? "",
      name: site?.name ?? "",
      kind: site?.kind ?? "warehouse",
      fulfilsOrders: site?.fulfilsOrders ?? true,
      line1: site?.address?.line1 ?? "",
      line2: site?.address?.line2 ?? "",
      postalCode: site?.address?.postalCode ?? "",
      city: site?.address?.city ?? "",
      country: site?.address?.country ?? "",
    },
    validate: {
      code: (value) => (CODE_PATTERN.test(value.trim().toUpperCase()) ? null : t("codeHint")),
      name: (value) => (value.trim() ? null : t("required")),
      country: (value) => (value === "" || /^[A-Za-z]{2}$/.test(value) ? null : t("countryHint")),
    },
  });

  const save = useMutation({
    mutationFn: (values: SiteFormValues) => {
      const address = Object.fromEntries(
        (["line1", "line2", "postalCode", "city", "country"] as const)
          .map((field) => [field, field === "country" ? values[field].trim().toUpperCase() : values[field].trim()])
          .filter(([, value]) => value !== ""),
      );
      const input: InventorySiteInput = {
        code: values.code.trim().toUpperCase(),
        name: values.name.trim(),
        kind: values.kind,
        fulfilsOrders: values.fulfilsOrders,
        ...(Object.keys(address).length > 0 ? { address } : {}),
      };
      return site ? updateSite(site.id, { ...input, status: site.status }, site.revision) : createSite(input);
    },
    onSuccess: async (saved) => {
      notifications.show({ color: "teal", title: t(site ? "siteSaved" : "siteCreated"), message: saved.name });
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
      if (code === "site_code_taken" || code === "code_invalid") {
        form.setFieldError("code", t(`refusal.${code}`));
        return;
      }
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
      setProblem(refusalMessage(t, error));
    },
  });

  return (
    <Modal opened={opened} onClose={onClose} title={t(site ? "editSite" : "newSite")} size="lg">
      <form
        onSubmit={form.onSubmit((values) => {
          setProblem(undefined);
          save.mutate(values);
        })}
      >
        <Stack gap="md">
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <TextInput
              label={t("code")}
              description={t("codeHint")}
              withAsterisk
              maxLength={16}
              {...form.getInputProps("code")}
            />
            <TextInput label={t("name")} withAsterisk {...form.getInputProps("name")} />
            <Select
              label={t("siteKind")}
              withAsterisk
              allowDeselect={false}
              data={siteKinds.map((value) => ({ value, label: t(`siteKind_${value}`) }))}
              {...form.getInputProps("kind")}
              onChange={(value) => {
                if (!value) return;
                form.setFieldValue("kind", value as InventorySiteKind);
                // A new site follows its kind's default until saved (orders design O45).
                if (!site) form.setFieldValue("fulfilsOrders", value === "warehouse");
              }}
            />
          </SimpleGrid>
          <Switch
            label={t("fulfilsOrders")}
            description={t("fulfilsOrdersHint")}
            {...form.getInputProps("fulfilsOrders", { type: "checkbox" })}
          />
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <TextInput label={t("addressLine1")} {...form.getInputProps("line1")} />
            <TextInput label={t("addressLine2")} {...form.getInputProps("line2")} />
            <TextInput label={t("postalCode")} {...form.getInputProps("postalCode")} />
            <TextInput label={t("city")} {...form.getInputProps("city")} />
            <TextInput
              label={t("country")}
              description={t("countryHint")}
              maxLength={2}
              {...form.getInputProps("country")}
            />
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
