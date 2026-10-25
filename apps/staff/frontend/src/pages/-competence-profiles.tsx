import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Modal,
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
import { randomId } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  competenceCategoriesQueryOptions,
  competenceProfilesQueryOptions,
  competencesQueryOptions,
  createCompetenceProfile,
  type StaffCompetence,
  type StaffCompetenceCategory,
  type StaffCompetenceProfile,
  type StaffCompetenceProfileItemInput,
  updateCompetenceProfile,
} from "../api/competences";
import { type ApiError, ApiValidationError, STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalText } from "../lib/absence";
import { competenceName, levelOptions, offeredCompetences } from "../lib/competences";
import { isRevisionConflict, listPath } from "../lib/errors";

const PROFILE_KEY = /^[a-z0-9-]{1,40}$/;

/** The most items a profile holds (plan reading 28). */
const MAX_ITEMS = 50;

/**
 * The competence profiles (staff competence design S12) in Settings,
 * `staff:manage`'s: what a position needs, as a named list of competences,
 * each with a minimum level when it has one and whether it must be valid.
 * A profile is assigned on the person's Competence tab and made a
 * department's default on the department; archived, it asks nothing of
 * anyone until restored. The key is set once.
 */
export const CompetenceProfilesTable = () => {
  const { t, locale } = useI18n("staff");
  const { data, isPending, error } = useQuery(competenceProfilesQueryOptions());
  const [modal, setModal] = useState<{ row?: StaffCompetenceProfile } | null>(null);
  const nameOf = (row: StaffCompetenceProfile) => competenceName(row, locale);

  return (
    <Card withBorder padding="lg" radius="md" component="section" aria-label={t("competenceProfiles")}>
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <Stack gap={0} maw={640}>
            <Title order={5}>{t("competenceProfiles")}</Title>
            <Text size="sm" c="dimmed">
              {t("competenceProfilesDescription")}
            </Text>
          </Stack>
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModal({})}>
            {t("newProfile")}
          </Button>
        </Group>
        {error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProfiles")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={3} rowHeight={44} />}
        {data?.length === 0 && <EmptyState title={t("noProfiles")} />}
        {data && data.length > 0 && (
          <Table.ScrollContainer minWidth={640}>
            <Table aria-label={t("competenceProfiles")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("categoryKey")}</Table.Th>
                  <Table.Th>{t("categoryNameEn")}</Table.Th>
                  <Table.Th>{t("categoryNameNb")}</Table.Th>
                  <Table.Th>{t("profileItems")}</Table.Th>
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
                    <Table.Td>{row.items.length}</Table.Td>
                    <Table.Td>
                      {row.archivedAt && (
                        <Badge variant="light" color="gray">
                          {t("archived")}
                        </Badge>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <ActionIcon
                        variant="subtle"
                        aria-label={t("editRow", { name: nameOf(row) })}
                        onClick={() => setModal({ row })}
                      >
                        <IconPencil size={16} />
                      </ActionIcon>
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
          title={modal?.row ? t("editProfile", { name: nameOf(modal.row) }) : t("newProfile")}
          size="lg"
        >
          {modal && <ProfileForm key={modal.row?.id ?? "new"} row={modal.row} onClose={() => setModal(null)} />}
        </Modal>
      </Stack>
    </Card>
  );
};

interface ItemValues {
  key: string;
  competenceId: string | null;
  minLevel: string | null;
  mustBeValid: boolean;
}

interface ProfileValues {
  key: string;
  en: string;
  nb: string;
  archived: boolean;
  items: ItemValues[];
}

/** Whether an item's competence is judged valid at all: a certificate with a validity. */
const hasValidity = (competence?: StaffCompetence) =>
  competence?.kind === "certificate" && competence.validityMonths !== undefined;

const ProfileForm = ({ row, onClose }: { row?: StaffCompetenceProfile; onClose: () => void }) => {
  const { t, locale } = useI18n("staff");
  const queryClient = useQueryClient();
  const catalogue = useQuery(competencesQueryOptions({ includeHidden: true, includeArchived: true }));
  const categories = useQuery(competenceCategoriesQueryOptions());
  const [banner, setBanner] = useState<string[]>([]);
  const name = (value: string) => (value.trim() && value.trim().length <= 100 ? null : t("nameRequired100"));
  const form = useForm<ProfileValues>({
    initialValues: {
      key: row?.key ?? "",
      en: row?.names.en ?? "",
      nb: row?.names.nb ?? "",
      archived: row?.archivedAt !== undefined,
      items: (row?.items ?? []).map((item) => ({
        key: randomId(),
        competenceId: String(item.competenceId),
        minLevel: item.minLevel === undefined ? null : String(item.minLevel),
        mustBeValid: item.mustBeValid,
      })),
    },
    validate: {
      key: (value) => (row || PROFILE_KEY.test(value.trim()) ? null : t("categoryKeyInvalid")),
      en: name,
      nb: name,
      items: {
        competenceId: (value) => (value ? null : t("profileItemCompetenceRequired")),
      },
    },
  });
  const competenceOf = (id: string | null) => catalogue.data?.find((one) => String(one.id) === id);
  // A level is asked of a competence that has one — and shown where an item still holds one, to be cleared.
  const levelShown = (item: ItemValues) => Boolean(competenceOf(item.competenceId)?.hasLevel) || item.minLevel !== null;

  const save = useMutation({
    mutationFn: (values: ProfileValues) => {
      const names = { en: values.en.trim(), nb: values.nb.trim() };
      // Each item as the form holds it: a level only where one was chosen (a new competence clears it), and
      // the validity rule as stored — true for a new item — so an unchanged item is sent unchanged.
      const items: StaffCompetenceProfileItemInput[] = values.items.map((item) => ({
        competenceId: Number(item.competenceId),
        ...(item.minLevel !== null ? { minLevel: Number(item.minLevel) } : {}),
        mustBeValid: item.mustBeValid,
      }));
      return row
        ? updateCompetenceProfile(row.id, { names, archived: values.archived, items }, row.revision)
        : createCompetenceProfile({ key: values.key.trim(), names, items });
    },
    onSuccess: async (saved) => {
      notifications.show({
        color: "teal",
        title: t("profileSaved", { name: competenceName(saved, locale) }),
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
      if (!(failure instanceof ApiValidationError)) {
        setBanner([refusalText(failure, t)]);
        return;
      }
      // Each field error on the control it is about — an item's at its row —
      // and the rest said on the banner. A coded refusal is the catalogue
      // judged (judgeProfileItems): there an item's competence is unavailable
      // and its level not allowed, each in the catalog's words, on the field
      // the errors name; a plain refusal is the server's text.
      const coded = ["competence_unavailable", "level_not_allowed"].includes((failure as ApiError).code ?? "");
      const wordsOf = (field: string, message: string) => {
        if (coded && /^items\[\d+\]\.competenceId$/.test(field)) return t("refusal.competence_unavailable");
        if (coded && /^items\[\d+\]\.minLevel$/.test(field)) return t("refusal.level_not_allowed");
        return message;
      };
      const errors: Record<string, string> = {};
      const rest: string[] = [];
      for (const [field, message] of Object.entries(failure.fieldErrors)) {
        const words = wordsOf(field, message);
        const path = field === "names.en" ? "en" : field === "names.nb" ? "nb" : listPath(field);
        const item = /^items\.(\d+)\.(competenceId|minLevel|mustBeValid)$/.exec(path);
        const shown =
          ["key", "en", "nb"].includes(path) ||
          (item !== null &&
            Number(item[1]) < form.values.items.length &&
            (item[2] !== "minLevel" || levelShown(form.values.items[Number(item[1])])));
        if (shown) errors[path] = words;
        else rest.push(words);
      }
      form.setErrors(errors);
      setBanner(rest);
    },
  });

  // The offered catalogue, and each item's own competence even if no longer offered, by category.
  const offered = offeredCompetences(catalogue.data ?? []);
  const optionsFor = (own: string | null) => {
    const shown = [...offered];
    const current = competenceOf(own);
    if (current && !shown.includes(current)) shown.push(current);
    const order = new Map((categories.data ?? []).map((category, index) => [category.id, index]));
    return [...new Set(shown.map((one) => one.categoryId))]
      .sort((a, b) => (order.get(a) ?? Number.MAX_SAFE_INTEGER) - (order.get(b) ?? Number.MAX_SAFE_INTEGER))
      .map((categoryId) => ({
        group: categoryName(categories.data ?? [], categoryId, locale, t("competenceCategoryUnknown")),
        items: shown
          .filter((one) => one.categoryId === categoryId)
          .map((one) => {
            const named = competenceName(one, locale);
            return {
              value: String(one.id),
              label: one.archivedAt ? t("competenceArchivedName", { name: named }) : named,
            };
          }),
      }));
  };

  return (
    <form
      onSubmit={form.onSubmit((values) => {
        setBanner([]);
        save.mutate(values);
      })}
    >
      <Stack>
        {banner.length > 0 && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            <Stack gap={2}>
              {banner.map((line) => (
                <Text key={line} size="sm">
                  {line}
                </Text>
              ))}
            </Stack>
          </Alert>
        )}
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
          <TextInput label={t("nameInEnglish")} withAsterisk {...form.getInputProps("en")} />
          <TextInput label={t("nameInNorwegian")} withAsterisk {...form.getInputProps("nb")} />
        </SimpleGrid>
        {row && (
          <Switch
            label={t("archived")}
            description={t("profileArchivedDescription")}
            {...form.getInputProps("archived", { type: "checkbox" })}
          />
        )}
        <Stack gap="xs" component="section" aria-label={t("profileItems")}>
          <Title order={6}>{t("profileItems")}</Title>
          {form.values.items.length === 0 && (
            <Text size="sm" c="dimmed">
              {t("profileNoItems")}
            </Text>
          )}
          {form.values.items.map((item, index) => {
            const competence = competenceOf(item.competenceId);
            return (
              <Group key={item.key} align="flex-end" gap="xs" wrap="wrap">
                <Select
                  label={t("competencePick")}
                  placeholder={t("competencePickPlaceholder")}
                  nothingFoundMessage={t("competenceNothingFound")}
                  searchable
                  allowDeselect={false}
                  style={{ flex: "2 1 200px" }}
                  data={optionsFor(item.competenceId)}
                  {...form.getInputProps(`items.${index}.competenceId`)}
                  onChange={(value) => {
                    // A new competence asks anew: no level, and valid unless told otherwise.
                    form.setFieldValue(`items.${index}.competenceId`, value);
                    form.setFieldValue(`items.${index}.minLevel`, null);
                    form.setFieldValue(`items.${index}.mustBeValid`, true);
                    form.clearFieldError(`items.${index}.minLevel`);
                  }}
                />
                {levelShown(item) && (
                  <Select
                    label={t("minLevel")}
                    clearable
                    style={{ flex: "1 1 140px" }}
                    data={levelOptions(t)}
                    {...form.getInputProps(`items.${index}.minLevel`)}
                  />
                )}
                {hasValidity(competence) && (
                  <Switch
                    label={t("mustBeValid")}
                    mb={6}
                    {...form.getInputProps(`items.${index}.mustBeValid`, { type: "checkbox" })}
                  />
                )}
                <ActionIcon
                  variant="subtle"
                  color="red"
                  mb={4}
                  aria-label={t("removeProfileItem", { number: index + 1 })}
                  onClick={() => form.removeListItem("items", index)}
                >
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
            );
          })}
          <Group>
            <Button
              variant="light"
              size="xs"
              leftSection={<IconPlus size={14} />}
              disabled={form.values.items.length >= MAX_ITEMS}
              onClick={() =>
                form.insertListItem("items", { key: randomId(), competenceId: null, minLevel: null, mustBeValid: true })
              }
            >
              {t("addProfileItem")}
            </Button>
          </Group>
        </Stack>
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          {/* The catalogue says what each item is; nothing is saved before it is read. */}
          <Button type="submit" loading={save.isPending} disabled={!catalogue.data}>
            {row ? t("save") : t("add")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

const categoryName = (categories: StaffCompetenceCategory[], id: number, locale: string, unknown: string) => {
  const category = categories.find((one) => one.id === id);
  return category ? competenceName(category, locale) : unknown;
};
