import { Button, Group, Modal, Select, Stack, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type StaffEmployment, type StaffRelationshipType, updateEmployment } from "../api/employments";
import { type StaffStructure, staffMetaQueryOptions } from "../api/meta";
import { STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { conflictField, fieldRefusals, isRevisionConflict } from "../lib/errors";
import { relationshipTypes } from "../lib/terms";

export interface EmploymentEditModalProps {
  employment: StaffEmployment;
  structure?: StaffStructure;
  opened: boolean;
  onClose: () => void;
}

interface EmploymentFormValues {
  startDate: string | null;
  businessUnitId: string | null;
  relationshipType: StaffRelationshipType;
  contractSignedAt: string | null;
  /**
   * The holiday scheme (delivery B), `"default"` for none: the PUT is a full
   * replace, and a scheme left out is the default's, so an untouched one is
   * sent back as the employment read it.
   */
  holidaySchemeId: string;
}

/**
 * *Edit employment* (`PUT /employments/{id}`, `If-Match`): the start — the
 * first terms move with it — the business unit, the kind of relationship and
 * the day the contract was signed and the holiday scheme, replaced whole.
 * The employment number is
 * shown and never changes; the end has its own action.
 */
export const EmploymentEditModal = ({ employment, structure, opened, onClose }: EmploymentEditModalProps) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={t("editEmploymentTitle", { number: employment.employmentNumber ?? "" })}
      fullScreen={phone}
    >
      {opened && <EmploymentForm employment={employment} structure={structure} onDone={onClose} />}
    </Modal>
  );
};

const EmploymentForm = ({
  employment,
  structure,
  onDone,
}: {
  employment: StaffEmployment;
  structure?: StaffStructure;
  onDone: () => void;
}) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const form = useForm<EmploymentFormValues>({
    initialValues: {
      startDate: employment.startDate,
      businessUnitId: employment.businessUnitId ? String(employment.businessUnitId) : null,
      relationshipType: employment.relationshipType ?? "ordinary",
      contractSignedAt: employment.contractSignedAt ?? null,
      holidaySchemeId: employment.holidaySchemeId === undefined ? "default" : String(employment.holidaySchemeId),
    },
    validate: { startDate: (value) => (value ? null : t("startDateRequired")) },
  });
  // Archived units leave the picker — except the one the employment is already in.
  const units = (structure?.businessUnits ?? []).filter(
    (unit) => !unit.archivedAt || unit.id === employment.businessUnitId,
  );
  // Likewise the holiday schemes: an archived one only when the employment follows it.
  const { data: meta } = useQuery(staffMetaQueryOptions());
  const schemes = (meta?.holidaySchemes ?? []).filter(
    (scheme) => !scheme.archivedAt || scheme.id === employment.holidaySchemeId,
  );
  const defaultScheme = schemes.find((scheme) => scheme.isDefault);

  const save = useMutation({
    mutationFn: (values: EmploymentFormValues) =>
      updateEmployment(
        employment.id,
        {
          startDate: values.startDate as string,
          businessUnitId: Number(values.businessUnitId ?? employment.businessUnitId),
          relationshipType: values.relationshipType,
          ...(values.contractSignedAt ? { contractSignedAt: values.contractSignedAt } : {}),
          ...(values.holidaySchemeId !== "default" ? { holidaySchemeId: Number(values.holidaySchemeId) } : {}),
        },
        employment.revision,
      ),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("employmentSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        onDone();
        return;
      }
      // A later terms row the new start would pass is a 409 about the start.
      const placed = conflictField(error, { terms_before_start: "startDate" });
      if (placed) return form.setErrors(placed);
      const { fields, rest } = fieldRefusals(error, [
        "startDate",
        "businessUnitId",
        "relationshipType",
        "contractSignedAt",
        "holidaySchemeId",
      ]);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSaveEmployment"), message: rest.join(" ") });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Text size="sm" c="dimmed">
          {t("employmentNumberFixed", { number: employment.employmentNumber ?? "" })}
        </Text>
        <DateInput
          label={t("startDate")}
          description={t("startDateMovesFirstTerms")}
          withAsterisk
          valueFormat={t("dateInputFormat")}
          {...form.getInputProps("startDate")}
        />
        {structure && (
          <Select
            label={t("businessUnit")}
            allowDeselect={false}
            data={units.map((unit) => ({
              value: String(unit.id),
              label: unit.archivedAt ? t("archivedName", { name: unit.name }) : unit.name,
            }))}
            {...form.getInputProps("businessUnitId")}
          />
        )}
        <Select
          label={t("relationshipType")}
          allowDeselect={false}
          data={relationshipTypes.map((value) => ({ value, label: t(`relationshipType_${value}`) }))}
          {...form.getInputProps("relationshipType")}
        />
        <DateInput
          label={t("contractSignedAt")}
          valueFormat={t("dateInputFormat")}
          clearable
          {...form.getInputProps("contractSignedAt")}
        />
        <Select
          label={t("holidayScheme")}
          description={t("holidaySchemeDescription")}
          allowDeselect={false}
          data={[
            { value: "default", label: t("holidaySchemeDefault", { name: defaultScheme?.name ?? "" }) },
            ...schemes
              .filter((scheme) => !scheme.isDefault || scheme.id === employment.holidaySchemeId)
              .map((scheme) => ({
                value: String(scheme.id),
                label: scheme.archivedAt ? t("archivedName", { name: scheme.name }) : scheme.name,
              })),
          ]}
          {...form.getInputProps("holidaySchemeId")}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
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
