import { Button, Group, Modal, Select, SimpleGrid, Stack, Switch, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { personQueryOptions } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import {
  createStructureRow,
  type StaffBusinessUnit,
  type StaffDepartment,
  type StaffLegalEntity,
  type StaffLocation,
  structureQueryOptions,
  updateStructureRow,
} from "../api/structure";
import { PersonPicker } from "../components/person-picker";
import "../i18n";
import { fieldRefusals, isRevisionConflict } from "../lib/errors";

/** Adding a row to one of the four tables, or changing the one the caller just read off it. */
export type StructureModalState =
  | { kind: "legal-entities"; row?: StaffLegalEntity }
  | { kind: "business-units"; row?: StaffBusinessUnit }
  | { kind: "departments"; row?: StaffDepartment }
  | { kind: "locations"; row?: StaffLocation };

export interface StructureFormModalProps {
  state: StructureModalState | null;
  onClose: () => void;
}

const titleKeys = {
  "legal-entities": ["addLegalEntityTitle", "editLegalEntityTitle"],
  "business-units": ["addBusinessUnitTitle", "editBusinessUnitTitle"],
  departments: ["addDepartmentTitle", "editDepartmentTitle"],
  locations: ["addLocationTitle", "editLocationTitle"],
} as const;

/** One row of the company's structure (design S6). Nothing is deleted; a row no longer in use is archived. */
export const StructureFormModal = ({ state, onClose }: StructureFormModalProps) => {
  const { t } = useI18n("staff");
  const [addKey, editKey] = state ? titleKeys[state.kind] : ["", ""];
  return (
    <Modal opened={state !== null} onClose={onClose} title={state ? t(state.row ? editKey : addKey) : ""} centered>
      {state?.kind === "legal-entities" && (
        <LegalEntityForm row={state.row} onClose={onClose} key={state.row?.id ?? "new"} />
      )}
      {state?.kind === "business-units" && (
        <BusinessUnitForm row={state.row} onClose={onClose} key={state.row?.id ?? "new"} />
      )}
      {state?.kind === "departments" && (
        <DepartmentForm row={state.row} onClose={onClose} key={state.row?.id ?? "new"} />
      )}
      {state?.kind === "locations" && <LocationForm row={state.row} onClose={onClose} key={state.row?.id ?? "new"} />}
    </Modal>
  );
};

/** An optional text: trimmed, and left out when empty — the contract clears a field left out. */
const optional = <K extends string>(key: K, value: string): Partial<Record<K, string>> =>
  value.trim() ? ({ [key]: value.trim() } as Record<K, string>) : {};

const required = (message: string) => (value: string) => (value.trim() ? null : message);

/**
 * The save every form here shares: a create or a revision-guarded replace,
 * the blanket invalidation, and the server's field errors put on the fields
 * they name — anything else said out loud.
 */
const useSave = <V,>(
  onClose: () => void,
  shown: readonly string[],
  setErrors: (errors: Record<string, string>) => void,
  write: (values: V) => Promise<{ name: string }>,
) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: write,
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("rowSaved", { name: saved.name }), message: "" });
      onClose();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        // The row moved on since the form opened: close it on the fresh
        // table rather than keep editing a version that is gone.
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        onClose();
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, shown);
      setErrors(fields);
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotSave"), message: rest.join(" ") });
    },
  });
};

const FormButtons = ({ onClose, loading }: { onClose: () => void; loading: boolean }) => {
  const { t } = useI18n("staff");
  return (
    <Group justify="flex-end">
      <Button type="button" variant="default" onClick={onClose}>
        {t("cancel")}
      </Button>
      <Button type="submit" loading={loading}>
        {t("save")}
      </Button>
    </Group>
  );
};

/**
 * The default flag, offered where it can be set: a new row, or a row that is
 * not the default. The flag is never taken off the default — another row is
 * made the default instead — so the default's own form only says that it is.
 */
const DefaultSwitch = ({ isDefault, inputProps }: { isDefault?: boolean; inputProps: object }) => {
  const { t } = useI18n("staff");
  if (isDefault) return <Text size="sm">{t("isTheDefault")}</Text>;
  return <Switch label={t("makeTheDefault")} description={t("makeTheDefaultDescription")} {...inputProps} />;
};

const LegalEntityForm = ({ row, onClose }: { row?: StaffLegalEntity; onClose: () => void }) => {
  const { t } = useI18n("staff");
  const form = useForm({
    initialValues: { name: row?.name ?? "", orgNumber: row?.orgNumber ?? "", isDefault: false },
    validate: { name: required(t("nameRequired")) },
  });
  const save = useSave(onClose, ["name", "orgNumber", "isDefault"], form.setErrors, (values: typeof form.values) => {
    const input = {
      name: values.name.trim(),
      ...optional("orgNumber", values.orgNumber),
      ...(values.isDefault ? { isDefault: true } : {}),
    };
    return row
      ? updateStructureRow("legal-entities", row.id, input, row.revision)
      : createStructureRow("legal-entities", input);
  });
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <TextInput label={t("name")} withAsterisk data-autofocus {...form.getInputProps("name")} />
        <TextInput
          label={t("orgNumber")}
          description={t("orgNumberDescription")}
          {...form.getInputProps("orgNumber")}
        />
        <DefaultSwitch isDefault={row?.isDefault} inputProps={form.getInputProps("isDefault", { type: "checkbox" })} />
        <FormButtons onClose={onClose} loading={save.isPending} />
      </Stack>
    </form>
  );
};

const BusinessUnitForm = ({ row, onClose }: { row?: StaffBusinessUnit; onClose: () => void }) => {
  const { t } = useI18n("staff");
  const { data: entities } = useQuery(structureQueryOptions("legal-entities"));
  const form = useForm({
    initialValues: {
      name: row?.name ?? "",
      legalEntityId: row
        ? String(row.legalEntityId)
        : (entities?.find((entity) => entity.isDefault)?.id.toString() ?? null),
      orgNumber: row?.orgNumber ?? "",
      isDefault: false,
    },
    validate: {
      name: required(t("nameRequired")),
      legalEntityId: (value) => (value ? null : t("legalEntityRequired")),
    },
  });
  const save = useSave(
    onClose,
    ["name", "legalEntityId", "orgNumber", "isDefault"],
    form.setErrors,
    (values: typeof form.values) => {
      const input = {
        name: values.name.trim(),
        legalEntityId: Number(values.legalEntityId),
        ...optional("orgNumber", values.orgNumber),
        ...(values.isDefault ? { isDefault: true } : {}),
      };
      return row
        ? updateStructureRow("business-units", row.id, input, row.revision)
        : createStructureRow("business-units", input);
    },
  );
  // Archived entities leave the picker; the one the unit already sits under stays nameable.
  const options = (entities ?? [])
    .filter((entity) => !entity.archivedAt || entity.id === row?.legalEntityId)
    .map((entity) => ({ value: String(entity.id), label: entity.name }));
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <TextInput label={t("name")} withAsterisk data-autofocus {...form.getInputProps("name")} />
        <Select
          label={t("legalEntity")}
          withAsterisk
          allowDeselect={false}
          data={options}
          {...form.getInputProps("legalEntityId")}
        />
        <TextInput
          label={t("orgNumber")}
          description={t("orgNumberDescription")}
          {...form.getInputProps("orgNumber")}
        />
        <DefaultSwitch isDefault={row?.isDefault} inputProps={form.getInputProps("isDefault", { type: "checkbox" })} />
        <FormButtons onClose={onClose} loading={save.isPending} />
      </Stack>
    </form>
  );
};

const DepartmentForm = ({ row, onClose }: { row?: StaffDepartment; onClose: () => void }) => {
  const { t } = useI18n("staff");
  const { data: departments } = useQuery(structureQueryOptions("departments"));
  const { data: manager } = useQuery({
    ...personQueryOptions(row?.managerPersonId ?? 0),
    enabled: Boolean(row?.managerPersonId),
  });
  const form = useForm({
    initialValues: {
      code: row?.code ?? "",
      name: row?.name ?? "",
      managerPersonId: row?.managerPersonId ?? null,
      parentId: row?.parentId ? String(row.parentId) : null,
      costCentre: row?.costCentre ?? "",
      division: row?.division ?? "",
    } as {
      code: string;
      name: string;
      managerPersonId: number | null;
      parentId: string | null;
      costCentre: string;
      division: string;
    },
    validate: {
      code: (value) => (/^[A-Za-z0-9-]{1,20}$/.test(value.trim()) ? null : t("departmentCodeInvalid")),
      name: required(t("nameRequired")),
    },
  });
  const save = useSave(
    onClose,
    ["code", "name", "managerPersonId", "parentId", "costCentre", "division"],
    form.setErrors,
    (values: typeof form.values) => {
      const input = {
        code: values.code.trim(),
        name: values.name.trim(),
        ...(values.managerPersonId ? { managerPersonId: values.managerPersonId } : {}),
        ...(values.parentId ? { parentId: Number(values.parentId) } : {}),
        ...optional("costCentre", values.costCentre),
        ...optional("division", values.division),
      };
      return row
        ? updateStructureRow("departments", row.id, input, row.revision)
        : createStructureRow("departments", input);
    },
  );
  // Neither itself nor an archived one — unless it already sits under it. The
  // server refuses one below it (department_cycle) and says so.
  const parents = (departments ?? [])
    .filter((department) => department.id !== row?.id)
    .filter((department) => !department.archivedAt || department.id === row?.parentId)
    .map((department) => ({ value: String(department.id), label: `${department.name} (${department.code})` }));
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t("code")}
            description={t("departmentCodeDescription")}
            withAsterisk
            data-autofocus
            {...form.getInputProps("code")}
          />
          <TextInput label={t("name")} withAsterisk {...form.getInputProps("name")} />
        </SimpleGrid>
        <PersonPicker
          label={t("manager")}
          description={t("departmentManagerDescription")}
          value={form.values.managerPersonId}
          onChange={(id) => form.setFieldValue("managerPersonId", id)}
          selected={manager}
          error={form.errors.managerPersonId as string | undefined}
        />
        <Select label={t("parentDepartment")} clearable data={parents} {...form.getInputProps("parentId")} />
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput label={t("costCentre")} {...form.getInputProps("costCentre")} />
          <TextInput label={t("division")} {...form.getInputProps("division")} />
        </SimpleGrid>
        <FormButtons onClose={onClose} loading={save.isPending} />
      </Stack>
    </form>
  );
};

const LocationForm = ({ row, onClose }: { row?: StaffLocation; onClose: () => void }) => {
  const { t } = useI18n("staff");
  const form = useForm({
    initialValues: {
      name: row?.name ?? "",
      street: row?.street ?? "",
      postalCode: row?.postalCode ?? "",
      city: row?.city ?? "",
      country: row?.country ?? "",
    },
    validate: {
      name: required(t("nameRequired")),
      country: (value) => (value.trim() === "" || /^[A-Za-z]{2}$/.test(value.trim()) ? null : t("countryInvalid")),
    },
  });
  const save = useSave(
    onClose,
    ["name", "street", "postalCode", "city", "country"],
    form.setErrors,
    (values: typeof form.values) => {
      const input = {
        name: values.name.trim(),
        ...optional("street", values.street),
        ...optional("postalCode", values.postalCode),
        ...optional("city", values.city),
        ...(values.country.trim() ? { country: values.country.trim().toUpperCase() } : {}),
      };
      return row
        ? updateStructureRow("locations", row.id, input, row.revision)
        : createStructureRow("locations", input);
    },
  );
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <TextInput label={t("name")} withAsterisk data-autofocus {...form.getInputProps("name")} />
        <TextInput label={t("street")} {...form.getInputProps("street")} />
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput label={t("postalCode")} {...form.getInputProps("postalCode")} />
          <TextInput label={t("city")} {...form.getInputProps("city")} />
        </SimpleGrid>
        <TextInput label={t("country")} description={t("countryDescription")} {...form.getInputProps("country")} />
        <FormButtons onClose={onClose} loading={save.isPending} />
      </Stack>
    </form>
  );
};
