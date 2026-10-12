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
  createDocumentCategory,
  documentCategoriesQueryOptions,
  type StaffDocumentCategory,
  type StaffDocumentClass,
  updateDocumentCategory,
} from "../api/documents";
import { type ApiError, ApiValidationError, STAFF_QUERY_KEY } from "../api/request";
import { CategoryBadge } from "../components/category-badge";
import "../i18n";
import { fieldRefusals, isRevisionConflict, refusalMessage } from "../lib/errors";

const CATEGORY_KEY = /^[a-z0-9-]{1,40}$/;
const documentClasses: StaffDocumentClass[] = ["A", "D", "E"];

/**
 * The refusals of a category's change in the reader's words: a seed never
 * changes (`seed_immutable`), and a category documents are filed under is
 * not archived (`category_in_use`).
 */
const useCategoryRefusal = () => {
  const { t } = useI18n("staff");
  return (error: Error, name: string): string => {
    const code = (error as ApiError).code;
    if (code === "seed_immutable") return t("seedImmutable");
    if (code === "category_in_use") return t("categoryInUse", { name });
    return refusalMessage(error);
  };
};

/**
 * The document categories (design S14) in Settings, `staff:manage`'s: the
 * six seeds, fixed, and the company's own — whose names change and which
 * are archived and restored; the key and the class are set once, when the
 * category is created, and `restricted` follows from the class.
 */
export const DocumentCategoriesTable = () => {
  const { t, locale } = useI18n("staff");
  const queryClient = useQueryClient();
  const refusal = useCategoryRefusal();
  const { data, isPending, error } = useQuery(documentCategoriesQueryOptions());
  const [modal, setModal] = useState<{ row?: StaffDocumentCategory } | null>(null);
  const nameOf = (row: StaffDocumentCategory) => row.names[locale] ?? row.names.en;

  const archive = useMutation({
    mutationFn: ({ row, archived }: { row: StaffDocumentCategory; archived: boolean }) =>
      updateDocumentCategory(row.id, { names: row.names, archived }, row.revision),
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
        message: refusal(failure, nameOf(row)),
      });
    },
  });

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <Stack gap={0} maw={640}>
            <Title order={5}>{t("documentCategories")}</Title>
            <Text size="sm" c="dimmed">
              {t("documentCategoriesDescription")}
            </Text>
          </Stack>
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModal({})}>
            {t("addDocumentCategory")}
          </Button>
        </Group>
        {error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadDocumentCategories")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={3} rowHeight={44} />}
        {data && (
          <Table.ScrollContainer minWidth={720}>
            <Table aria-label={t("documentCategories")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("categoryKey")}</Table.Th>
                  <Table.Th>{t("categoryNameEn")}</Table.Th>
                  <Table.Th>{t("categoryNameNb")}</Table.Th>
                  <Table.Th>{t("categoryClass")}</Table.Th>
                  <Table.Th>{t("categoryRestricted")}</Table.Th>
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
                      <CategoryBadge documentClass={row.class} />
                    </Table.Td>
                    <Table.Td>{row.restricted ? t("yes") : t("no")}</Table.Td>
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
                          {row.archivedAt ? (
                            <Button
                              size="compact-xs"
                              variant="subtle"
                              aria-label={t("restoreRow", { name: nameOf(row) })}
                              onClick={() => archive.mutate({ row, archived: false })}
                            >
                              {t("restore")}
                            </Button>
                          ) : (
                            <Button
                              size="compact-xs"
                              variant="subtle"
                              color="red"
                              aria-label={t("archiveRow", { name: nameOf(row) })}
                              onClick={() => archive.mutate({ row, archived: true })}
                            >
                              {t("archive")}
                            </Button>
                          )}
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
          title={modal?.row ? t("editDocumentCategoryTitle") : t("addDocumentCategoryTitle")}
          centered
        >
          {modal && <CategoryForm key={modal.row?.id ?? "new"} row={modal.row} onClose={() => setModal(null)} />}
        </Modal>
      </Stack>
    </Card>
  );
};

interface CategoryFormValues {
  key: string;
  en: string;
  nb: string;
  documentClass: StaffDocumentClass | null;
}

/** A new category takes all four; a company category's change takes its names, its key and class shown as they stay. */
const CategoryForm = ({ row, onClose }: { row?: StaffDocumentCategory; onClose: () => void }) => {
  const { t, locale } = useI18n("staff");
  const queryClient = useQueryClient();
  const refusal = useCategoryRefusal();
  const name = (value: string) => (value.trim() && value.trim().length <= 100 ? null : t("categoryNameRequired"));
  const form = useForm<CategoryFormValues>({
    initialValues: {
      key: row?.key ?? "",
      en: row?.names.en ?? "",
      nb: row?.names.nb ?? "",
      documentClass: row?.class ?? null,
    },
    validate: {
      key: (value) => (row || CATEGORY_KEY.test(value.trim()) ? null : t("categoryKeyInvalid")),
      en: name,
      nb: name,
      documentClass: (value) => (value ? null : t("categoryClassRequired")),
    },
  });

  const save = useMutation({
    mutationFn: (values: CategoryFormValues) => {
      const names = { en: values.en.trim(), nb: values.nb.trim() };
      return row
        ? updateDocumentCategory(row.id, { names, archived: row.archivedAt !== undefined }, row.revision)
        : createDocumentCategory({ key: values.key.trim(), names, class: values.documentClass as StaffDocumentClass });
    },
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("categorySaved", { name: saved.names[locale] }), message: "" });
      onClose();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        onClose();
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        return;
      }
      // The server keys the names as `names.en`; the form's paths are flat.
      const { fields, rest } = fieldRefusals(error, ["key", "names.en", "names.nb", "class"]);
      form.setErrors({
        ...(fields.key ? { key: fields.key } : {}),
        ...(fields["names.en"] ? { en: fields["names.en"] } : {}),
        ...(fields["names.nb"] ? { nb: fields["names.nb"] } : {}),
        ...(fields.class ? { documentClass: fields.class } : {}),
      });
      if (rest.length > 0)
        notifications.show({
          color: "red",
          title: t("couldNotSave"),
          message:
            error instanceof ApiValidationError
              ? rest.join(" ")
              : refusal(error, row ? row.names[locale] : form.values.en),
        });
    },
  });

  const chosen = form.values.documentClass;
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        {row ? (
          <Group gap="xs">
            <Text size="sm" ff="monospace">
              {row.key}
            </Text>
            <CategoryBadge documentClass={row.class} />
          </Group>
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
        {!row && (
          <Select
            label={t("categoryClass")}
            description={t("categoryClassDescription")}
            withAsterisk
            allowDeselect={false}
            data={documentClasses.map((value) => ({
              value,
              label: `${t(`class_${value}`)} — ${t(`classExplained_${value}`)}`,
            }))}
            {...form.getInputProps("documentClass")}
          />
        )}
        {chosen && (
          <Text size="sm" c="dimmed">
            {chosen === "E" ? t("categoryRestrictedYes") : t("categoryRestrictedNo")}
          </Text>
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
