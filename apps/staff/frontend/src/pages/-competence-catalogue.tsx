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
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  competenceCategoriesQueryOptions,
  competencesQueryOptions,
  createCompetence,
  createCompetenceCategory,
  type StaffCompetence,
  type StaffCompetenceCategory,
  type StaffCompetenceChange,
  type StaffCompetenceKind,
  updateCompetence,
  updateCompetenceCategory,
} from "../api/competences";
import { type ApiError, ApiValidationError, STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalText } from "../lib/absence";
import { competenceKinds, competenceName, kindLabel } from "../lib/competences";
import { fieldRefusals, isRevisionConflict } from "../lib/errors";

const CATEGORY_KEY = /^[a-z0-9-]{1,40}$/;
const COMPETENCE_KEY = /^[a-z0-9-]{1,60}$/;

type Translate = (key: string, values?: Record<string, unknown>) => string;

/** A refusal of a catalogue write in the reader's words: a seed's own sentence, a category in use by name, else the code's. */
const catalogueRefusal = (error: Error, t: Translate, seed: string, name: string): string => {
  const code = (error as ApiError).code;
  if (code === "seed_immutable") return t(seed);
  if (code === "category_in_use") return t("competenceCategoryInUse", { name });
  return refusalText(error, t);
};

/**
 * The competence categories (staff competence design S1) in Settings,
 * `staff:manage`'s: the five seeds, fixed, and the company's own — whose
 * names change and which are archived and restored; the key is set once.
 */
export const CompetenceCategoriesTable = () => {
  const { t, locale } = useI18n("staff");
  const queryClient = useQueryClient();
  const { data, isPending, error } = useQuery(competenceCategoriesQueryOptions());
  const [modal, setModal] = useState<{ row?: StaffCompetenceCategory } | null>(null);
  const nameOf = (row: StaffCompetenceCategory) => competenceName(row, locale);

  const archive = useMutation({
    mutationFn: ({ row, archived }: { row: StaffCompetenceCategory; archived: boolean }) =>
      updateCompetenceCategory(row.id, { names: row.names, archived }, row.revision),
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("categorySaved", { name: nameOf(saved) }), message: "" });
    },
    onError: async (failure, { row }) => {
      if (isRevisionConflict(failure)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        return;
      }
      notifications.show({
        color: "red",
        title: t("couldNotChangeCategory", { name: nameOf(row) }),
        message: catalogueRefusal(failure, t, "competenceCategorySeedImmutable", nameOf(row)),
      });
    },
  });

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <Stack gap={0} maw={640}>
            <Title order={5}>{t("competenceCategories")}</Title>
            <Text size="sm" c="dimmed">
              {t("competenceCategoriesDescription")}
            </Text>
          </Stack>
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModal({})}>
            {t("addCompetenceCategory")}
          </Button>
        </Group>
        {error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadCompetenceCategories")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={3} rowHeight={44} />}
        {data && (
          <Table.ScrollContainer minWidth={600}>
            <Table aria-label={t("competenceCategories")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("categoryKey")}</Table.Th>
                  <Table.Th>{t("categoryNameEn")}</Table.Th>
                  <Table.Th>{t("categoryNameNb")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th>{t("rowActions")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {data.map((row) => (
                  <Table.Tr key={row.id} c={row.archivedAt ? "dimmed" : undefined}>
                    <Table.Td>
                      <Text size="sm" ff="monospace">
                        {row.key}
                      </Text>
                    </Table.Td>
                    <Table.Td>{row.names.en}</Table.Td>
                    <Table.Td>{row.names.nb}</Table.Td>
                    <Table.Td>
                      <Group gap={4}>
                        {row.seeded && (
                          <Badge variant="light" color="blue">
                            {t("categorySeeded")}
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
                      {/* A seed never changes: it carries no action at all. */}
                      {!row.seeded && (
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
                            color={row.archivedAt ? undefined : "red"}
                            aria-label={t(row.archivedAt ? "restoreRow" : "archiveRow", { name: nameOf(row) })}
                            loading={archive.isPending && archive.variables?.row.id === row.id}
                            onClick={() => archive.mutate({ row, archived: !row.archivedAt })}
                          >
                            {t(row.archivedAt ? "restore" : "archive")}
                          </Button>
                        </Group>
                      )}
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
          title={modal?.row ? t("editCompetenceCategoryTitle") : t("addCompetenceCategoryTitle")}
          centered
        >
          {modal && <CategoryForm key={modal.row?.id ?? "new"} row={modal.row} onClose={() => setModal(null)} />}
        </Modal>
      </Stack>
    </Card>
  );
};

interface CategoryValues {
  key: string;
  en: string;
  nb: string;
}

const CategoryForm = ({ row, onClose }: { row?: StaffCompetenceCategory; onClose: () => void }) => {
  const { t, locale } = useI18n("staff");
  const queryClient = useQueryClient();
  const name = (value: string) => (value.trim() && value.trim().length <= 100 ? null : t("categoryNameRequired"));
  const form = useForm<CategoryValues>({
    initialValues: { key: row?.key ?? "", en: row?.names.en ?? "", nb: row?.names.nb ?? "" },
    validate: {
      key: (value) => (row || CATEGORY_KEY.test(value.trim()) ? null : t("categoryKeyInvalid")),
      en: name,
      nb: name,
    },
  });
  const save = useMutation({
    mutationFn: (values: CategoryValues) => {
      const names = { en: values.en.trim(), nb: values.nb.trim() };
      return row
        ? updateCompetenceCategory(row.id, { names, archived: row.archivedAt !== undefined }, row.revision)
        : createCompetenceCategory({ key: values.key.trim(), names });
    },
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({
        color: "teal",
        title: t("categorySaved", { name: competenceName(saved, locale) }),
        message: "",
      });
      onClose();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        onClose();
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, ["key", "names.en", "names.nb"]);
      form.setErrors({
        ...(fields.key ? { key: fields.key } : {}),
        ...(fields["names.en"] ? { en: fields["names.en"] } : {}),
        ...(fields["names.nb"] ? { nb: fields["names.nb"] } : {}),
      });
      if (rest.length > 0)
        notifications.show({
          color: "red",
          title: t("couldNotSave"),
          message:
            error instanceof ApiValidationError
              ? rest.join(" ")
              : catalogueRefusal(error, t, "competenceCategorySeedImmutable", form.values.en),
        });
    },
  });
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        {row ? (
          <Text size="sm" ff="monospace">
            {row.key}
          </Text>
        ) : (
          <TextInput
            label={t("categoryKey")}
            description={t("categoryKeyDescription")}
            withAsterisk
            {...form.getInputProps("key")}
          />
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput label={t("categoryNameEn")} withAsterisk {...form.getInputProps("en")} />
          <TextInput label={t("categoryNameNb")} withAsterisk {...form.getInputProps("nb")} />
        </SimpleGrid>
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

/** A competence's whole replace from the row as read — what the form does not touch is sent as it was. */
const changeOf = (row: StaffCompetence): StaffCompetenceChange => ({
  categoryId: row.categoryId,
  names: row.names,
  hasLevel: row.hasLevel,
  hidden: row.hidden,
  archived: row.archivedAt !== undefined,
  ...(row.validityMonths !== undefined ? { validityMonths: row.validityMonths } : {}),
  ...(row.reminderLeadDays !== undefined ? { reminderLeadDays: row.reminderLeadDays } : {}),
  ...(row.issuerHint !== undefined ? { issuerHint: row.issuerHint } : {}),
});

/**
 * The competence catalogue (staff competence design S1, S2) in Settings,
 * `staff:manage`'s: every competence, hidden and archived ones included,
 * with its category, kind, level, validity and reminder. A seed keeps its
 * key, kind, level and validity, and is renamed and hidden; a company
 * competence is HR's whole but for its key and kind, and is archived and
 * restored too.
 */
export const CompetencesTable = () => {
  const { t, locale } = useI18n("staff");
  const queryClient = useQueryClient();
  const { data, isPending, error } = useQuery(competencesQueryOptions({ includeHidden: true, includeArchived: true }));
  const categories = useQuery(competenceCategoriesQueryOptions());
  const [modal, setModal] = useState<{ row?: StaffCompetence } | null>(null);
  const nameOf = (row: StaffCompetence) => competenceName(row, locale);
  const categoryOf = (id: number) => {
    const category = categories.data?.find((one) => one.id === id);
    return category ? competenceName(category, locale) : t("notSet");
  };

  const change = useMutation({
    mutationFn: ({ row, patch }: { row: StaffCompetence; patch: Partial<StaffCompetenceChange> }) =>
      updateCompetence(row.id, { ...changeOf(row), ...patch }, row.revision),
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("competenceSaved", { name: nameOf(saved) }), message: "" });
    },
    onError: async (failure, { row }) => {
      notifications.show({
        color: isRevisionConflict(failure) ? "yellow" : "red",
        title: t("couldNotChangeCategory", { name: nameOf(row) }),
        message: catalogueRefusal(failure, t, "competenceSeedImmutable", nameOf(row)),
      });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
  });
  const busy = (row: StaffCompetence) => change.isPending && change.variables?.row.id === row.id;

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <Stack gap={0} maw={640}>
            <Title order={5}>{t("competences")}</Title>
            <Text size="sm" c="dimmed">
              {t("competenceCatalogueDescription")}
            </Text>
          </Stack>
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModal({})}>
            {t("newCompetence")}
          </Button>
        </Group>
        {error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadCompetenceCatalogue")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={4} rowHeight={44} />}
        {data && (
          <Table.ScrollContainer minWidth={900}>
            <Table aria-label={t("competences")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("name")}</Table.Th>
                  <Table.Th>{t("competenceCategory")}</Table.Th>
                  <Table.Th>{t("competenceKind")}</Table.Th>
                  <Table.Th>{t("competenceLevelShort")}</Table.Th>
                  <Table.Th>{t("competenceValidityShort")}</Table.Th>
                  <Table.Th>{t("competenceLeadShort")}</Table.Th>
                  <Table.Th>{t("competenceIssuerHint")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th>{t("rowActions")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {data.map((row) => (
                  <Table.Tr key={row.id} c={row.hidden || row.archivedAt ? "dimmed" : undefined}>
                    <Table.Td>{nameOf(row)}</Table.Td>
                    <Table.Td>{categoryOf(row.categoryId)}</Table.Td>
                    <Table.Td>{kindLabel(t, row.kind)}</Table.Td>
                    <Table.Td>{row.hasLevel ? t("yes") : t("no")}</Table.Td>
                    <Table.Td>{row.validityMonths ?? t("notSet")}</Table.Td>
                    <Table.Td>{row.reminderLeadDays ?? t("notSet")}</Table.Td>
                    <Table.Td>
                      <Text size="sm" style={{ overflowWrap: "anywhere" }}>
                        {row.issuerHint ?? t("notSet")}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4}>
                        {row.seeded && (
                          <Badge variant="light" color="blue">
                            {t("categorySeeded")}
                          </Badge>
                        )}
                        {row.hidden && (
                          <Badge variant="light" color="gray">
                            {t("typeHidden")}
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
                          aria-label={t("editRow", { name: nameOf(row) })}
                          onClick={() => setModal({ row })}
                        >
                          <IconPencil size={16} />
                        </ActionIcon>
                        <Button
                          size="compact-xs"
                          variant="subtle"
                          aria-label={t(row.hidden ? "showRow" : "hideRow", { name: nameOf(row) })}
                          loading={busy(row)}
                          onClick={() => change.mutate({ row, patch: { hidden: !row.hidden } })}
                        >
                          {t(row.hidden ? "showType" : "hideType")}
                        </Button>
                        {/* A seed is hidden, never archived: it is the statutory vocabulary. */}
                        {!row.seeded && (
                          <Button
                            size="compact-xs"
                            variant="subtle"
                            color={row.archivedAt ? undefined : "red"}
                            aria-label={t(row.archivedAt ? "restoreRow" : "archiveRow", { name: nameOf(row) })}
                            loading={busy(row)}
                            onClick={() => change.mutate({ row, patch: { archived: !row.archivedAt } })}
                          >
                            {t(row.archivedAt ? "restore" : "archive")}
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
          title={modal?.row ? t("editRow", { name: nameOf(modal.row) }) : t("newCompetence")}
          size="lg"
        >
          {modal && (
            <CompetenceForm
              key={modal.row?.id ?? "new"}
              row={modal.row}
              categories={categories.data ?? []}
              onClose={() => setModal(null)}
            />
          )}
        </Modal>
      </Stack>
    </Card>
  );
};

interface CompetenceValues {
  categoryId: string | null;
  key: string;
  en: string;
  nb: string;
  kind: StaffCompetenceKind;
  hasLevel: boolean;
  validityMonths: number | string;
  reminderLeadDays: number | string;
  issuerHint: string;
}

const CompetenceForm = ({
  row,
  categories,
  onClose,
}: {
  row?: StaffCompetence;
  categories: StaffCompetenceCategory[];
  onClose: () => void;
}) => {
  const { t, locale } = useI18n("staff");
  const queryClient = useQueryClient();
  const [banner, setBanner] = useState<string | null>(null);
  // A seed changes its names, the reminder, the issuer and whether it is hidden alone.
  const fixed = row?.seeded ?? false;
  const name = (value: string) => (value.trim() && value.trim().length <= 100 ? null : t("nameRequired100"));
  const form = useForm<CompetenceValues>({
    initialValues: {
      categoryId: row ? String(row.categoryId) : null,
      key: row?.key ?? "",
      en: row?.names.en ?? "",
      nb: row?.names.nb ?? "",
      kind: row?.kind ?? "skill",
      hasLevel: row?.hasLevel ?? false,
      validityMonths: row?.validityMonths ?? "",
      reminderLeadDays: row?.reminderLeadDays ?? "",
      issuerHint: row?.issuerHint ?? "",
    },
    validate: {
      categoryId: (value) => (value ? null : t("competenceCategoryRequired")),
      key: (value) => (row || COMPETENCE_KEY.test(value.trim()) ? null : t("competenceKeyInvalid")),
      en: name,
      nb: name,
      validityMonths: (value, values) =>
        value === "" || values.kind === "skill" || (Number(value) >= 1 && Number(value) <= 240)
          ? null
          : t("competenceValidityMonthsInvalid"),
      reminderLeadDays: (value, values) =>
        value === "" ||
        (values.kind === "certificate" && values.validityMonths !== "" && Number(value) >= 0 && Number(value) <= 365)
          ? null
          : t("competenceLeadDaysInvalid"),
      issuerHint: (value) => (value.trim().length > 200 ? t("textTooLong", { max: 200 }) : null),
    },
  });
  const certificate = form.values.kind === "certificate";
  const offered = categories.filter((category) => !category.archivedAt || category.id === row?.categoryId);

  const save = useMutation({
    mutationFn: (values: CompetenceValues) => {
      const names = { en: values.en.trim(), nb: values.nb.trim() };
      const validity =
        values.kind === "certificate" && values.validityMonths !== ""
          ? {
              validityMonths: Number(values.validityMonths),
              ...(values.reminderLeadDays !== "" ? { reminderLeadDays: Number(values.reminderLeadDays) } : {}),
            }
          : {};
      const issuerHint = values.issuerHint.trim() ? { issuerHint: values.issuerHint.trim() } : {};
      if (row) {
        // A seed sends its fixed fields back exactly as read.
        const base = fixed
          ? {
              categoryId: row.categoryId,
              hasLevel: row.hasLevel,
              ...(row.validityMonths !== undefined ? { validityMonths: row.validityMonths } : {}),
              ...(values.reminderLeadDays !== "" ? { reminderLeadDays: Number(values.reminderLeadDays) } : {}),
            }
          : { categoryId: Number(values.categoryId), hasLevel: values.hasLevel, ...validity };
        return updateCompetence(
          row.id,
          { ...base, names, ...issuerHint, hidden: row.hidden, archived: row.archivedAt !== undefined },
          row.revision,
        );
      }
      return createCompetence({
        categoryId: Number(values.categoryId),
        key: values.key.trim(),
        names,
        kind: values.kind,
        hasLevel: values.hasLevel,
        ...validity,
        ...issuerHint,
      });
    },
    onSuccess: async (saved) => {
      notifications.show({
        color: "teal",
        title: t("competenceSaved", { name: competenceName(saved, locale) }),
        message: "",
      });
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
      const shown = [
        "categoryId",
        "key",
        "names.en",
        "names.nb",
        "hasLevel",
        "validityMonths",
        "reminderLeadDays",
        "issuerHint",
      ];
      const code = (failure as ApiError).code;
      if (failure instanceof ApiValidationError && !code) {
        const { fields, rest } = fieldRefusals(failure, shown);
        const { "names.en": en, "names.nb": nb, ...others } = fields;
        form.setErrors({ ...others, ...(en ? { en } : {}), ...(nb ? { nb } : {}) });
        if (rest.length > 0) setBanner(rest.join(" "));
        return;
      }
      setBanner(
        catalogueRefusal(failure, t, "competenceSeedImmutable", row ? competenceName(row, locale) : form.values.en),
      );
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
        {fixed && <Text size="sm">{t("competenceSeededExplained")}</Text>}
        {row ? (
          <Text size="sm" ff="monospace">
            {row.key}
          </Text>
        ) : (
          <TextInput
            label={t("categoryKey")}
            description={t("competenceKeyDescription")}
            withAsterisk
            {...form.getInputProps("key")}
          />
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput label={t("nameInEnglish")} withAsterisk {...form.getInputProps("en")} />
          <TextInput label={t("nameInNorwegian")} withAsterisk {...form.getInputProps("nb")} />
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Select
            label={t("competenceCategory")}
            withAsterisk
            disabled={fixed}
            allowDeselect={false}
            data={offered.map((category) => ({ value: String(category.id), label: competenceName(category, locale) }))}
            {...form.getInputProps("categoryId")}
          />
          <Select
            label={t("competenceKind")}
            description={row ? t("kindFixed") : undefined}
            disabled={Boolean(row)}
            allowDeselect={false}
            data={competenceKinds.map((kind) => ({ value: kind, label: kindLabel(t, kind) }))}
            {...form.getInputProps("kind")}
          />
        </SimpleGrid>
        <Switch
          label={t("competenceHasLevel")}
          description={t("competenceHasLevelDescription")}
          disabled={fixed}
          {...form.getInputProps("hasLevel", { type: "checkbox" })}
        />
        {certificate && (
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <NumberInput
              label={t("competenceValidityMonths")}
              description={t("competenceValidityMonthsDescription")}
              disabled={fixed}
              min={1}
              max={240}
              allowDecimal={false}
              allowNegative={false}
              {...form.getInputProps("validityMonths")}
            />
            <NumberInput
              label={t("competenceLeadDays")}
              description={t("competenceLeadDaysDescription")}
              min={0}
              max={365}
              allowDecimal={false}
              allowNegative={false}
              {...form.getInputProps("reminderLeadDays")}
            />
          </SimpleGrid>
        )}
        <TextInput
          label={t("competenceIssuerHint")}
          description={t("competenceIssuerHintDescription")}
          {...form.getInputProps("issuerHint")}
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
