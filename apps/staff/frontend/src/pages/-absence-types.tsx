import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Modal,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useLocale } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  absenceTypesQueryOptions,
  createAbsenceType,
  type StaffAbsenceTypeChange,
  type StaffAmeldingLeave,
  updateAbsenceType,
} from "../api/absence-settings";
import type { StaffAbsenceType } from "../api/meta";
import { ApiValidationError, STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalText } from "../lib/absence";
import { isRevisionConflict } from "../lib/errors";
import { useStaffFormat } from "../lib/format";

/** The a-melding's leave descriptions and permittering, in NAV's spellings (Revision 2, Q4a). */
const ameldingLeaves: StaffAmeldingLeave[] = [
  "permisjonMedForeldrepenger",
  "permisjonVedMilitaertjeneste",
  "utdanningspermisjonLovfestet",
  "utdanningspermisjonIkkeLovfestet",
  "andreLovfestedePermisjoner",
  "andreIkkeLovfestedePermisjoner",
  "permittering",
];
/** The kinds a company type may be; the sick kinds are the seeds'. */
const companyKinds = ["holiday", "leave", "time_off", "other"] as const;

/** A type's whole replace but for its key, kind and unit, from the row as read — what the form does not touch is sent as it was. */
const changeOf = (row: StaffAbsenceType): StaffAbsenceTypeChange => ({
  names: row.names,
  sort: row.sort,
  hidden: row.hidden,
  allowsPercent: row.allowsPercent,
  drawsHoliday: row.drawsHoliday,
  needsDecision: row.needsDecision,
  paid: row.paid,
  visibility: row.visibility,
  ...(row.ameldingLeave ? { ameldingLeave: row.ameldingLeave } : {}),
});

/**
 * Settings' absence types (design S8, §7): the seeds — the statutory
 * vocabulary — shown read-only with their a-melding mapping and visibility,
 * whose names, order and hidden flag alone change, and the company's own
 * types, editable but for the key, kind and unit. The reserved `absent`,
 * which the health erase writes, is never listed. Widening an HR-only type
 * that absences name is refused (`absence_type_in_use`), in words.
 */
export const AbsenceTypesTable = () => {
  const { t } = useStaffFormat();
  const locale = useLocale();
  const queryClient = useQueryClient();
  const { data, isPending, error } = useQuery(absenceTypesQueryOptions());
  const [modal, setModal] = useState<{ row?: StaffAbsenceType } | null>(null);
  const nameOf = (row: StaffAbsenceType) => (locale === "nb" ? row.names.nb : row.names.en);
  const toggle = useMutation({
    mutationFn: (row: StaffAbsenceType) =>
      updateAbsenceType(row.id, { ...changeOf(row), hidden: !row.hidden }, row.revision),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: async (failure) => {
      notifications.show({
        color: isRevisionConflict(failure) ? "yellow" : "red",
        title: t("couldNotSave"),
        message: refusalText(failure, t),
      });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
  });
  const rows = (data ?? []).filter((row) => !row.reserved);
  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <Stack gap={0} maw={640}>
            <Title order={5}>{t("absenceTypes")}</Title>
            <Text size="sm" c="dimmed">
              {t("absenceTypesDescription")}
            </Text>
          </Stack>
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModal({})}>
            {t("newAbsenceType")}
          </Button>
        </Group>
        {error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadAbsenceTypes")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={4} rowHeight={44} />}
        {data && (
          <Table.ScrollContainer minWidth={820}>
            <Table aria-label={t("absenceTypes")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("name")}</Table.Th>
                  <Table.Th>{t("absenceKind")}</Table.Th>
                  <Table.Th>{t("absenceVisibility")}</Table.Th>
                  <Table.Th>{t("ameldingLeave")}</Table.Th>
                  <Table.Th>{t("absenceTypeRules")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th>{t("rowActions")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {rows.map((row) => (
                  <Table.Tr key={row.id} c={row.hidden ? "dimmed" : undefined}>
                    <Table.Td>{nameOf(row)}</Table.Td>
                    <Table.Td>
                      {t(`absenceKind_${row.kind}`)}
                      {row.unit === "hours" ? ` · ${t("absenceUnit_hours")}` : ""}
                    </Table.Td>
                    <Table.Td>{t(`absenceVisibilityShort_${row.visibility}`)}</Table.Td>
                    <Table.Td>
                      <Text size="xs" ff="monospace">
                        {row.ameldingLeave ?? t("notSet")}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="xs">
                        {[
                          t(row.needsDecision ? "typeNeedsDecision" : "typeIsNotification"),
                          ...(row.drawsHoliday ? [t("typeDrawsHoliday")] : []),
                          ...(row.allowsPercent ? [t("typeAllowsPercent")] : []),
                          t(row.paid ? "typePaid" : "typeUnpaid"),
                        ].join(" · ")}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4}>
                        {row.seeded && (
                          <Badge variant="light" color="blue">
                            {t("typeSeeded")}
                          </Badge>
                        )}
                        {row.hidden && (
                          <Badge variant="light" color="gray">
                            {t("typeHidden")}
                          </Badge>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4} wrap="nowrap">
                        <ActionIcon
                          variant="subtle"
                          aria-label={t("editRow", { name: nameOf(row) })}
                          onClick={() => setModal({ row })}
                        >
                          <IconPencil size={16} />
                        </ActionIcon>
                        <Button
                          size="compact-xs"
                          variant="subtle"
                          aria-label={t(row.hidden ? "showRow" : "hideRow", { name: nameOf(row) })}
                          loading={toggle.isPending && toggle.variables?.id === row.id}
                          onClick={() => toggle.mutate(row)}
                        >
                          {t(row.hidden ? "showType" : "hideType")}
                        </Button>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
        <Modal
          opened={modal !== null}
          onClose={() => setModal(null)}
          title={modal?.row ? t("editRow", { name: nameOf(modal.row) }) : t("newAbsenceType")}
          size="lg"
        >
          {modal && <TypeForm key={modal.row?.id ?? "new"} row={modal.row} onClose={() => setModal(null)} />}
        </Modal>
      </Stack>
    </Card>
  );
};

interface TypeValues {
  key: string;
  nameEn: string;
  nameNb: string;
  kind: string;
  unit: "days" | "hours";
  paid: boolean;
  needsDecision: boolean;
  drawsHoliday: boolean;
  allowsPercent: boolean;
  visibility: "absent" | "hr_only";
  ameldingLeave: StaffAmeldingLeave | null;
  sort: number | string;
  hidden: boolean;
}

const TypeForm = ({ row, onClose }: { row?: StaffAbsenceType; onClose: () => void }) => {
  const { t } = useStaffFormat();
  const queryClient = useQueryClient();
  const [banner, setBanner] = useState<string | null>(null);
  // A seed changes its names, its order and whether it is hidden alone.
  const fixed = row?.seeded ?? false;
  const form = useForm<TypeValues>({
    initialValues: {
      key: row?.key ?? "",
      nameEn: row?.names.en ?? "",
      nameNb: row?.names.nb ?? "",
      kind: row?.kind ?? "leave",
      unit: row?.unit ?? "days",
      paid: row?.paid ?? true,
      needsDecision: row?.needsDecision ?? true,
      drawsHoliday: row?.drawsHoliday ?? false,
      allowsPercent: row?.allowsPercent ?? false,
      visibility: row?.visibility ?? "absent",
      ameldingLeave: row?.ameldingLeave ?? null,
      sort: row?.sort ?? "",
      hidden: row?.hidden ?? false,
    },
    validate: {
      key: (value) => (row || /^[a-z0-9_]{1,40}$/.test(value) ? null : t("absenceTypeKeyInvalid")),
      nameEn: (value) => (value.trim() && value.trim().length <= 100 ? null : t("nameRequired100")),
      nameNb: (value) => (value.trim() && value.trim().length <= 100 ? null : t("nameRequired100")),
      sort: (value) =>
        value === ""
          ? row
            ? t("sortInvalid")
            : null
          : Number(value) >= 1 && Number(value) <= 1000
            ? null
            : t("sortInvalid"),
    },
  });
  const days = form.values.unit === "days";
  const save = useMutation({
    mutationFn: (values: TypeValues) => {
      const names = { en: values.nameEn.trim(), nb: values.nameNb.trim() };
      const rules = {
        paid: values.paid,
        drawsHoliday: days && values.drawsHoliday,
        allowsPercent: days && values.allowsPercent,
        needsDecision: values.needsDecision,
        visibility: values.visibility,
        ...(values.ameldingLeave ? { ameldingLeave: values.ameldingLeave } : {}),
      };
      if (row)
        return updateAbsenceType(
          row.id,
          // A seed sends its rules back exactly as read; the server refuses any
          // other. A company type sends the form's rules — an a-melding mapping
          // cleared in the form is left out, which clears it.
          {
            ...(fixed ? changeOf(row) : rules),
            names,
            sort: Number(values.sort),
            hidden: values.hidden,
          },
          row.revision,
        );
      return createAbsenceType({
        key: values.key,
        names,
        kind: values.kind as StaffAbsenceType["kind"],
        unit: values.unit,
        ...rules,
        ...(values.sort === "" ? {} : { sort: Number(values.sort) }),
        ...(values.hidden ? { hidden: true } : {}),
      });
    },
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("absenceTypeSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onClose();
    },
    onError: async (failure) => {
      if (isRevisionConflict(failure)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        onClose();
        return;
      }
      if (failure instanceof ApiValidationError && !(failure as { code?: string }).code) {
        setBanner(Object.values(failure.fieldErrors).join(" ") || failure.message);
        return;
      }
      setBanner(refusalText(failure, t));
    },
  });
  return (
    <form
      onSubmit={form.onSubmit((values) => {
        setBanner(null);
        save.mutate(values);
      })}
    >
      <Stack>
        {banner && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {banner}
          </Alert>
        )}
        {fixed && <Text size="sm">{t("seededTypeExplained")}</Text>}
        {!row && (
          <TextInput
            label={t("absenceTypeKey")}
            description={t("absenceTypeKeyDescription")}
            withAsterisk
            {...form.getInputProps("key")}
          />
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput label={t("nameInEnglish")} withAsterisk {...form.getInputProps("nameEn")} />
          <TextInput label={t("nameInNorwegian")} withAsterisk {...form.getInputProps("nameNb")} />
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Select
            label={t("absenceKind")}
            description={row ? t("kindFixed") : undefined}
            disabled={Boolean(row)}
            allowDeselect={false}
            data={(row && !companyKinds.includes(row.kind as never) ? [row.kind] : companyKinds).map((kind) => ({
              value: kind,
              label: t(`absenceKind_${kind}`),
            }))}
            {...form.getInputProps("kind")}
          />
          <Select
            label={t("absenceUnit")}
            disabled={Boolean(row)}
            allowDeselect={false}
            data={(["days", "hours"] as const).map((unit) => ({ value: unit, label: t(`absenceUnit_${unit}`) }))}
            {...form.getInputProps("unit")}
          />
        </SimpleGrid>
        <Select
          label={t("absenceVisibility")}
          disabled={fixed}
          allowDeselect={false}
          data={(["absent", "hr_only"] as const).map((value) => ({ value, label: t(`absenceVisibility_${value}`) }))}
          {...form.getInputProps("visibility")}
        />
        <Select
          label={t("ameldingLeave")}
          description={t("ameldingLeaveDescription")}
          disabled={fixed}
          clearable
          // Mantine hides its clear button from assistive technology; this one is named and reachable.
          clearButtonProps={{ "aria-label": t("clearAmelding"), "aria-hidden": false, tabIndex: 0 }}
          data={ameldingLeaves}
          {...form.getInputProps("ameldingLeave")}
        />
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Switch
            label={t("typeNeedsDecisionField")}
            disabled={fixed}
            {...form.getInputProps("needsDecision", { type: "checkbox" })}
          />
          <Switch label={t("typePaidField")} disabled={fixed} {...form.getInputProps("paid", { type: "checkbox" })} />
          <Switch
            label={t("typeDrawsHolidayField")}
            disabled={fixed || !days}
            {...form.getInputProps("drawsHoliday", { type: "checkbox" })}
          />
          <Switch
            label={t("typeAllowsPercentField")}
            disabled={fixed || !days}
            {...form.getInputProps("allowsPercent", { type: "checkbox" })}
          />
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <NumberInput
            label={t("typeSort")}
            description={row ? undefined : t("typeSortDescription")}
            min={1}
            max={1000}
            allowDecimal={false}
            {...form.getInputProps("sort")}
          />
          <Switch
            label={t("typeHiddenField")}
            description={t("typeHiddenDescription")}
            {...form.getInputProps("hidden", { type: "checkbox" })}
          />
        </SimpleGrid>
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {row ? t("save") : t("add")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
