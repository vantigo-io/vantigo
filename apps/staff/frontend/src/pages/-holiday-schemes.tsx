import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Modal,
  NumberInput,
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
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  createHolidayScheme,
  holidaySchemesQueryOptions,
  type StaffHolidaySchemeChange,
  updateHolidayScheme,
} from "../api/absence-settings";
import type { StaffHolidayScheme } from "../api/meta";
import { ApiValidationError, STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalText } from "../lib/absence";
import { isRevisionConflict } from "../lib/errors";
import { useStaffFormat } from "../lib/format";

/** Virkedager (Monday to Saturday) as working days of a five-day week: ⌊v / 6⌋ × 5 + min(v mod 6, 5). */
const convertVirkedager = (v: number): number => Math.floor(v / 6) * 5 + Math.min(v % 6, 5);

/** The most a scheme may let be carried over: 12 plus the entitlement's days above 25 (ferieloven § 7 (3)). */
const carryOverCeiling = (entitlement: number) => 12 + Math.max(0, entitlement - 25);

/** A scheme's whole replace from the row as read — what the form does not touch is sent as it was. */
const changeOf = (row: StaffHolidayScheme): StaffHolidaySchemeChange => ({
  name: row.name,
  entitlementVirkedager: row.entitlementVirkedager,
  extraFrom60Virkedager: row.extraFrom60Virkedager,
  mainPeriodVirkedager: row.mainPeriodVirkedager,
  afterSeptemberVirkedager: row.afterSeptemberVirkedager,
  maxCarryOverVirkedager: row.maxCarryOverVirkedager,
  archived: row.archivedAt !== undefined,
});

/**
 * Settings' holiday schemes (design S9, §7): each with its figures in
 * virkedager and what they come to on a five-day week, the default marked.
 * The form holds ferieloven's floors — the server stays the judge — and a
 * scheme that is the default or that an employment follows is not archived
 * (`holiday_scheme_in_use`), said in words.
 */
export const HolidaySchemesTable = () => {
  const { t } = useStaffFormat();
  const queryClient = useQueryClient();
  const { data, isPending, error } = useQuery(holidaySchemesQueryOptions());
  const [modal, setModal] = useState<{ row?: StaffHolidayScheme } | null>(null);
  const archive = useMutation({
    mutationFn: ({ row, archived }: { row: StaffHolidayScheme; archived: boolean }) =>
      updateHolidayScheme(row.id, { ...changeOf(row), archived }, row.revision),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: async (failure, { row }) => {
      notifications.show({
        color: isRevisionConflict(failure) ? "yellow" : "red",
        title: t("couldNotChangeScheme", { name: row.name }),
        message: refusalText(failure, t),
      });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
  });
  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <Stack gap={0} maw={640}>
            <Title order={5}>{t("holidaySchemes")}</Title>
            <Text size="sm" c="dimmed">
              {t("holidaySchemesDescription")}
            </Text>
          </Stack>
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModal({})}>
            {t("newHolidayScheme")}
          </Button>
        </Group>
        {error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadHolidaySchemes")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={2} rowHeight={44} />}
        {data && (
          <Table.ScrollContainer minWidth={720}>
            <Table aria-label={t("holidaySchemes")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("name")}</Table.Th>
                  <Table.Th>{t("schemeEntitlement")}</Table.Th>
                  <Table.Th>{t("schemeMaxCarryOver")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th>{t("rowActions")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {data.map((row) => (
                  <Table.Tr key={row.id} c={row.archivedAt ? "dimmed" : undefined}>
                    <Table.Td>{row.name}</Table.Td>
                    <Table.Td>
                      {t("schemeConversion", {
                        virkedager: row.entitlementVirkedager,
                        days: row.converted.entitlement,
                      })}
                    </Table.Td>
                    <Table.Td>
                      {t("schemeConversion", {
                        virkedager: row.maxCarryOverVirkedager,
                        days: row.converted.maxCarryOver,
                      })}
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4}>
                        {row.isDefault && (
                          <Badge variant="light" color="teal">
                            {t("default")}
                          </Badge>
                        )}
                        {row.archivedAt && (
                          <Badge variant="light" color="gray">
                            {t("archived")}
                          </Badge>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4} wrap="nowrap">
                        <ActionIcon
                          variant="subtle"
                          aria-label={t("editRow", { name: row.name })}
                          onClick={() => setModal({ row })}
                        >
                          <IconPencil size={16} />
                        </ActionIcon>
                        {row.archivedAt ? (
                          <Button
                            size="compact-xs"
                            variant="subtle"
                            aria-label={t("restoreRow", { name: row.name })}
                            onClick={() => archive.mutate({ row, archived: false })}
                          >
                            {t("restore")}
                          </Button>
                        ) : (
                          <Button
                            size="compact-xs"
                            variant="subtle"
                            color="red"
                            aria-label={t("archiveRow", { name: row.name })}
                            loading={archive.isPending && archive.variables?.row.id === row.id}
                            onClick={() => archive.mutate({ row, archived: true })}
                          >
                            {t("archive")}
                          </Button>
                        )}
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
          title={modal?.row ? t("editRow", { name: modal.row.name }) : t("newHolidayScheme")}
          size="lg"
        >
          {modal && <SchemeForm key={modal.row?.id ?? "new"} row={modal.row} onClose={() => setModal(null)} />}
        </Modal>
      </Stack>
    </Card>
  );
};

interface SchemeValues {
  name: string;
  entitlement: number | string;
  extraFrom60: number | string;
  mainPeriod: number | string;
  afterSeptember: number | string;
  maxCarryOver: number | string;
  isDefault: boolean;
}

/** Whether `value` is a whole number from `min` to `max`. */
const within = (value: number | string, min: number, max: number) =>
  value !== "" && Number.isInteger(Number(value)) && Number(value) >= min && Number(value) <= max;

const SchemeForm = ({ row, onClose }: { row?: StaffHolidayScheme; onClose: () => void }) => {
  const { t } = useStaffFormat();
  const queryClient = useQueryClient();
  const [banner, setBanner] = useState<string | null>(null);
  const entitlementOf = (values: SchemeValues) =>
    within(values.entitlement, 25, 60) ? Number(values.entitlement) : 25;
  const form = useForm<SchemeValues>({
    initialValues: {
      name: row?.name ?? "",
      entitlement: row?.entitlementVirkedager ?? 25,
      extraFrom60: row?.extraFrom60Virkedager ?? 6,
      mainPeriod: row?.mainPeriodVirkedager ?? 18,
      afterSeptember: row?.afterSeptemberVirkedager ?? 6,
      maxCarryOver: row?.maxCarryOverVirkedager ?? 12,
      isDefault: row?.isDefault ?? false,
    },
    validate: {
      name: (value) => (value.trim() && value.trim().length <= 100 ? null : t("nameRequired100")),
      entitlement: (value) => (within(value, 25, 60) ? null : t("schemeEntitlementRange")),
      extraFrom60: (value) => (within(value, 6, 30) ? null : t("schemeExtraRange")),
      mainPeriod: (value, values) =>
        within(value, 18, entitlementOf(values)) ? null : t("schemeMainPeriodRange", { max: entitlementOf(values) }),
      afterSeptember: (value, values) =>
        within(value, 6, entitlementOf(values)) ? null : t("schemeAfterSeptemberRange", { max: entitlementOf(values) }),
      maxCarryOver: (value, values) =>
        within(value, 0, carryOverCeiling(entitlementOf(values)))
          ? null
          : t("schemeCarryOverRange", { max: carryOverCeiling(entitlementOf(values)) }),
    },
  });
  const save = useMutation({
    mutationFn: (values: SchemeValues) => {
      const figures = {
        name: values.name.trim(),
        entitlementVirkedager: Number(values.entitlement),
        extraFrom60Virkedager: Number(values.extraFrom60),
        mainPeriodVirkedager: Number(values.mainPeriod),
        afterSeptemberVirkedager: Number(values.afterSeptember),
        maxCarryOverVirkedager: Number(values.maxCarryOver),
        // Left out, the flag stays: only making it the default is sent.
        ...(values.isDefault && !row?.isDefault ? { isDefault: true } : {}),
      };
      return row
        ? updateHolidayScheme(row.id, { ...figures, archived: row.archivedAt !== undefined }, row.revision)
        : createHolidayScheme(figures);
    },
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("holidaySchemeSaved"), message: "" });
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
  const days = (value: number | string) => (value === "" ? undefined : convertVirkedager(Number(value)));
  const hint = (value: number | string) =>
    days(value) === undefined ? undefined : t("schemeConvertsTo", { days: days(value) });
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
        <Text size="sm" c="dimmed">
          {t("schemeFormExplained")}
        </Text>
        <TextInput label={t("name")} withAsterisk {...form.getInputProps("name")} />
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Stack gap={2}>
            <NumberInput
              label={t("schemeEntitlementField")}
              withAsterisk
              allowDecimal={false}
              allowNegative={false}
              {...form.getInputProps("entitlement")}
            />
            <Text size="xs" c="dimmed">
              {hint(form.values.entitlement)}
            </Text>
          </Stack>
          <NumberInput
            label={t("schemeExtraField")}
            description={hint(form.values.extraFrom60)}
            allowDecimal={false}
            allowNegative={false}
            {...form.getInputProps("extraFrom60")}
          />
          <NumberInput
            label={t("schemeMainPeriodField")}
            description={hint(form.values.mainPeriod)}
            allowDecimal={false}
            allowNegative={false}
            {...form.getInputProps("mainPeriod")}
          />
          <NumberInput
            label={t("schemeAfterSeptemberField")}
            description={hint(form.values.afterSeptember)}
            allowDecimal={false}
            allowNegative={false}
            {...form.getInputProps("afterSeptember")}
          />
          <NumberInput
            label={t("schemeMaxCarryOverField")}
            description={hint(form.values.maxCarryOver)}
            allowDecimal={false}
            allowNegative={false}
            {...form.getInputProps("maxCarryOver")}
          />
        </SimpleGrid>
        <Switch
          label={t("schemeIsDefault")}
          description={t("schemeIsDefaultDescription")}
          disabled={row?.isDefault}
          {...form.getInputProps("isDefault", { type: "checkbox" })}
        />
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
