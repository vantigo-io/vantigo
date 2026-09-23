import {
  ActionIcon,
  Alert,
  Button,
  ColorSwatch,
  Group,
  Modal,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { ApiValidationError } from "../api/request";
import { type CustomerTagSummary, customerTagsQueryOptions, deleteTag, TAG_COLORS, updateTag } from "../api/tags";
import "../i18n";

/**
 * The tag vocabulary's own editor (owner and tags design D3): rename, recolour
 * and delete, reached from the list page's Tag filter and only for a caller the
 * host says may edit (`customers:update`). It is the one place a tag is deleted
 * from, and the confirmation says how many customers carry it — which is why
 * `GET /customers/tags` answers a `customerCount` at all.
 *
 * Every mutation invalidates the broad `["customers"]` prefix rather than only
 * `["customers", "tags"]`: renaming or deleting a tag changes what the chips on
 * every customer row and every customer page say, and those live under other
 * keys.
 */
export const ManageTagsModal = ({ opened, onClose }: { opened: boolean; onClose: () => void }) => {
  const { t } = useI18n("customers");
  const { data: tags } = useQuery({ ...customerTagsQueryOptions(), enabled: opened });
  const [editing, setEditing] = useState<CustomerTagSummary | null>(null);
  const [deleting, setDeleting] = useState<CustomerTagSummary | null>(null);

  return (
    <Modal opened={opened} onClose={onClose} title={t("manageTags")} centered>
      <Stack>
        {(tags ?? []).length === 0 && <EmptyState size="sm" title={t("noTagsYet")} />}
        {(tags ?? []).length > 0 && (
          <Table>
            <Table.Tbody>
              {(tags ?? []).map((tag) => (
                <Table.Tr key={tag.id}>
                  <Table.Td>
                    <Group gap="xs" wrap="nowrap">
                      <ColorSwatch color={`var(--mantine-color-${tag.color ?? "gray"}-6)`} size={12} />
                      <Text size="sm">{tag.name}</Text>
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" c="dimmed">
                      {tag.customerCount === 0
                        ? t("tagOnNoCustomers")
                        : t("tagOnCustomers", { count: tag.customerCount })}
                    </Text>
                  </Table.Td>
                  <Table.Td w={80}>
                    <Group gap={4} justify="flex-end" wrap="nowrap">
                      <ActionIcon
                        variant="subtle"
                        color="gray"
                        aria-label={t("renameNamedTag", { name: tag.name })}
                        onClick={() => setEditing(tag)}
                      >
                        <IconPencil size={16} />
                      </ActionIcon>
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={t("deleteNamedTag", { name: tag.name })}
                        onClick={() => setDeleting(tag)}
                      >
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
        <TagForm tag={editing} onDone={() => setEditing(null)} />
        <DeleteTagConfirmation tag={deleting} onDone={() => setDeleting(null)} />
      </Stack>
    </Modal>
  );
};

interface TagFormValues {
  name: string;
  color: string;
}

/**
 * Renames and recolours the tag it is given. It is only ever rendered for an
 * existing tag — creation happens in the Overview tab's own multi-select, where
 * someone is already typing a name (design D3), and this modal deliberately has
 * no create form of its own — so there is one request it can send and no
 * create/edit branch. The colour `Select`'s own "no colour" entry is the `""`
 * sentinel the list page's filters use, for the same reason: a Mantine `Select`
 * needs a real string among its `data` to offer a row.
 */
const TagForm = ({ tag, onDone }: { tag: CustomerTagSummary | null; onDone: () => void }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const form = useForm<TagFormValues>({ initialValues: { name: tag?.name ?? "", color: tag?.color ?? "" } });
  const [seen, setSeen] = useState(tag);
  if (tag !== seen) {
    setSeen(tag);
    form.setValues({ name: tag?.name ?? "", color: tag?.color ?? "" });
    form.clearErrors();
  }

  const mutation = useMutation({
    mutationFn: (values: TagFormValues) =>
      updateTag(String(tag?.id), { name: values.name, color: values.color || null }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      onDone();
      notifications.show({ color: "teal", title: t("tagSaved"), message: t("tagSavedMessage") });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({ color: "red", title: t("tagCouldNotBeSaved"), message: error.message });
    },
  });

  if (!tag) return null;
  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack gap="xs">
        <TextInput label={t("tagName")} data-autofocus {...form.getInputProps("name")} />
        <Select
          label={t("tagColour")}
          allowDeselect={false}
          data={[
            { value: "", label: t("tagNoColour") },
            ...TAG_COLORS.map((c) => ({ value: c, label: t(`tagColour_${c}`) })),
          ]}
          {...form.getInputProps("color")}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={mutation.isPending}>
            {t("saveChanges")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/**
 * The delete confirmation, which exists to say what the delete will actually
 * do: a tag is removed from every customer carrying it (the table's cascade),
 * and `customerCount` is how many that is.
 */
const DeleteTagConfirmation = ({ tag, onDone }: { tag: CustomerTagSummary | null; onDone: () => void }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: () => deleteTag(String(tag?.id)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      onDone();
      notifications.show({ color: "teal", title: t("tagDeleted"), message: t("tagDeletedMessage") });
    },
    onError: (error) => notifications.show({ color: "red", title: t("tagCouldNotBeDeleted"), message: error.message }),
  });

  if (!tag) return null;
  return (
    <Alert color="red" title={t("deleteTag")}>
      <Stack gap="xs">
        <Text size="sm">
          {tag.customerCount === 0
            ? t("deleteTagNoCustomers", { name: tag.name })
            : t("deleteTagWarning", { name: tag.name, count: tag.customerCount })}
        </Text>
        <Group justify="flex-end">
          <Button size="xs" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button size="xs" color="red" loading={mutation.isPending} onClick={() => mutation.mutate()}>
            {t("deleteTag")}
          </Button>
        </Group>
      </Stack>
    </Alert>
  );
};
