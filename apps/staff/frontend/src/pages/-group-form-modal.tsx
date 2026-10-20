import {
  Button,
  Group,
  Modal,
  SegmentedControl,
  Select,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { type UseFormReturnType, useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import {
  createGroup,
  GROUP_KEY_PATTERN,
  groupRuleKinds,
  groupRuleKindsById,
  type StaffGroup,
  type StaffGroupInput,
  type StaffGroupKind,
  type StaffGroupRuleKind,
  updateGroup,
} from "../api/groups";
import { personQueryOptions } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import { structureQueryOptions } from "../api/structure";
import { PersonPicker } from "../components/person-picker";
import "../i18n";
import { fieldRefusals, isRevisionConflict } from "../lib/errors";
import { groupLinkOptions } from "../lib/routes";
import { employeeTypes } from "../lib/terms";

export interface GroupFormModalProps {
  opened: boolean;
  /** The group to change; absent for a new one. */
  group?: StaffGroup;
  onClose: () => void;
}

/** A new group, or a change of the one the page shows — the key set once, never changed. */
export const GroupFormModal = ({ opened, group, onClose }: GroupFormModalProps) => {
  const { t } = useI18n("staff");
  return (
    <Modal opened={opened} onClose={onClose} title={t(group ? "editGroupTitle" : "newGroupTitle")} centered size="lg">
      {opened && <GroupForm group={group} onClose={onClose} key={group?.revision ?? "new"} />}
    </Modal>
  );
};

interface GroupFormValues {
  key: string;
  name: string;
  description: string;
  kind: StaffGroupKind;
  rule: {
    kind: StaffGroupRuleKind | null;
    /** A department's, unit's, location's or person's id, as the selects hold it. */
    id: string | null;
    value: string | null;
    includeBelow: boolean;
  };
}

const shown = ["key", "name", "description", "kind", "rule", "rule.kind", "rule.id", "rule.value", "rule.includeBelow"];

/** What the request sends of the rule: `id` for the four that name a row, `value` for a type, `includeBelow` for a department. */
const ruleOf = (rule: GroupFormValues["rule"]): StaffGroupInput["rule"] => {
  if (!rule.kind) return undefined;
  return {
    kind: rule.kind,
    ...(groupRuleKindsById.includes(rule.kind) && rule.id ? { id: Number(rule.id) } : {}),
    ...(rule.kind === "employeeType" && rule.value ? { value: rule.value } : {}),
    ...(rule.kind === "department" && rule.includeBelow ? { includeBelow: true } : {}),
  };
};

const GroupForm = ({ group, onClose }: { group?: StaffGroup; onClose: () => void }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const form = useForm<GroupFormValues>({
    initialValues: {
      key: group?.key ?? "",
      name: group?.name ?? "",
      description: group?.description ?? "",
      kind: group?.kind ?? "listed",
      rule: {
        kind: group?.rule?.kind ?? null,
        id: group?.rule?.id === undefined ? null : String(group.rule.id),
        value: group?.rule?.value ?? null,
        includeBelow: group?.rule?.includeBelow ?? false,
      },
    },
    validate: {
      key: (value) => (group || GROUP_KEY_PATTERN.test(value.trim()) ? null : t("groupKeyInvalid")),
      name: (value) => (value.trim() ? null : t("nameRequired")),
      rule: {
        kind: (value, values) => (values.kind === "derived" && !value ? t("groupRuleRequired") : null),
        id: (value, values) =>
          values.kind === "derived" && values.rule.kind && groupRuleKindsById.includes(values.rule.kind) && !value
            ? t("groupRuleIdRequired")
            : null,
        value: (value, values) =>
          values.kind === "derived" && values.rule.kind === "employeeType" && !value ? t("groupRuleIdRequired") : null,
      },
    },
  });

  const save = useMutation({
    mutationFn: (values: GroupFormValues) => {
      const input: StaffGroupInput = {
        name: values.name.trim(),
        ...(values.description.trim() ? { description: values.description.trim() } : {}),
        kind: values.kind,
        ...(values.kind === "derived" ? { rule: ruleOf(values.rule) } : {}),
      };
      return group ? updateGroup(group.id, input, group.revision) : createGroup({ ...input, key: values.key.trim() });
    },
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("rowSaved", { name: saved.name }), message: "" });
      onClose();
      if (!group) navigate(groupLinkOptions(saved.id));
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        onClose();
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, shown);
      form.setErrors(fields);
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotSave"), message: rest.join(" ") });
    },
  });

  const ruleKind = form.values.rule.kind;
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        {group ? (
          <Text size="sm">{t("groupKeyFixed", { key: group.key })}</Text>
        ) : (
          <TextInput
            label={t("groupKey")}
            description={t("groupKeyDescription")}
            withAsterisk
            data-autofocus
            {...form.getInputProps("key")}
          />
        )}
        <TextInput label={t("name")} withAsterisk maxLength={120} {...form.getInputProps("name")} />
        <Textarea label={t("groupDescriptionField")} rows={2} maxLength={500} {...form.getInputProps("description")} />
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {t("groupKind")}
          </Text>
          <SegmentedControl
            aria-label={t("groupKind")}
            data={[
              { value: "listed", label: t("groupKind_listed") },
              { value: "derived", label: t("groupKind_derived") },
            ]}
            {...form.getInputProps("kind")}
          />
          <Text size="xs" c="dimmed">
            {t(form.values.kind === "listed" ? "groupKindListedExplained" : "groupKindDerivedExplained")}
          </Text>
        </Stack>
        {form.values.kind === "derived" && (
          <>
            <Select
              label={t("groupRule")}
              withAsterisk
              allowDeselect={false}
              data={groupRuleKinds.map((kind) => ({ value: kind, label: t(`groupRule_${kind}`) }))}
              {...form.getInputProps("rule.kind")}
              onChange={(next) => {
                form.setFieldValue("rule.kind", next as StaffGroupRuleKind | null);
                // Another kind names another table: what was picked for the last one means nothing to it.
                form.setFieldValue("rule.id", null);
                form.setFieldValue("rule.value", null);
                form.setFieldValue("rule.includeBelow", false);
              }}
            />
            {ruleKind && <RuleReference form={form} kind={ruleKind} current={group?.rule?.id} />}
          </>
        )}
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

type GroupFormApi = UseFormReturnType<GroupFormValues>;

/** The rule's reference by its kind: a department and *Include departments beneath*, a unit, a location, a type, a person. */
const RuleReference = ({ form, kind, current }: { form: GroupFormApi; kind: StaffGroupRuleKind; current?: number }) => {
  const { t } = useI18n("staff");
  const table =
    kind === "department"
      ? "departments"
      : kind === "businessUnit"
        ? "business-units"
        : kind === "location"
          ? "locations"
          : undefined;
  const rows = useQuery({ ...structureQueryOptions(table ?? "departments"), enabled: table !== undefined });
  const person = useQuery({
    ...personQueryOptions(Number(form.values.rule.id ?? current ?? 0)),
    enabled: kind === "reportsTo" && Boolean(form.values.rule.id),
  });

  if (kind === "employed")
    return (
      <Text size="sm" c="dimmed">
        {t("groupRuleEmployedExplained")}
      </Text>
    );
  if (kind === "employeeType")
    return (
      <Select
        label={t("groupRule_employeeType")}
        withAsterisk
        data={employeeTypes.map((value) => ({ value, label: t(`employeeType_${value}`) }))}
        {...form.getInputProps("rule.value")}
      />
    );
  if (kind === "reportsTo")
    return (
      <PersonPicker
        label={t("groupRuleManager")}
        description={t("groupRuleReportsToExplained")}
        value={form.values.rule.id ? Number(form.values.rule.id) : null}
        onChange={(id) => form.setFieldValue("rule.id", id === null ? null : String(id))}
        selected={person.data}
        error={form.errors["rule.id"] as string | undefined}
      />
    );
  // An archived row is admitted by the rule, but offered only when the rule already names it.
  const options = (rows.data ?? [])
    .filter((row) => !row.archivedAt || row.id === current)
    .map((row) => ({
      value: String(row.id),
      label: row.archivedAt ? t("archivedName", { name: row.name }) : row.name,
    }));
  return (
    <>
      <Select
        label={t(`groupRule_${kind}`)}
        withAsterisk
        searchable
        data={options}
        {...form.getInputProps("rule.id")}
      />
      {kind === "department" && (
        <Switch
          label={t("groupIncludeBelow")}
          description={t("groupIncludeBelowExplained")}
          {...form.getInputProps("rule.includeBelow", { type: "checkbox" })}
        />
      )}
    </>
  );
};
