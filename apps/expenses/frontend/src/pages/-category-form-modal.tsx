import { Button, Group, Modal, Stack, Switch, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { createExpenseCategory, type ExpenseCategory, updateExpenseCategory } from "../api/categories";
import { ApiValidationError, EXPENSES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";

/** Adding a category, or renaming the one the caller just read off the list. */
export type CategoryModalState = { mode: "create" } | { mode: "edit"; category: ExpenseCategory };

export interface CategoryFormModalProps {
  state: CategoryModalState | null;
  onClose: () => void;
}

/**
 * One expense category. The name is unique however it is cased, and a
 * category is never deleted — one that is no longer wanted is deactivated, so
 * the expenses booked on it keep their name.
 */
export const CategoryFormModal = ({ state, onClose }: CategoryFormModalProps) => {
  const { t } = useI18n("expenses");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editCategoryTitle") : t("addCategoryTitle")}
      centered
    >
      {state && (
        <CategoryForm key={state.mode === "edit" ? state.category.id : "create"} state={state} onClose={onClose} />
      )}
    </Modal>
  );
};

const CategoryForm = ({ state, onClose }: CategoryFormModalProps & { state: CategoryModalState }) => {
  const { t } = useI18n("expenses");
  const queryClient = useQueryClient();
  const editing = state.mode === "edit" ? state.category : undefined;

  const form = useForm({
    initialValues: { name: editing?.name ?? "", active: editing?.active ?? true },
    validate: { name: (value) => (value.trim() ? null : t("categoryNameRequired")) },
  });

  const save = useMutation({
    mutationFn: (values: typeof form.values) =>
      editing
        ? updateExpenseCategory(editing.id, {
            name: values.name.trim(),
            active: values.active,
            position: editing.position,
          })
        : createExpenseCategory({ name: values.name.trim() }),
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("categorySaved"), message: saved.name });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError && error.fieldErrors.name) {
        form.setErrors({ name: error.fieldErrors.name });
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSaveCategory"), message: refusalMessage(error) });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <TextInput label={t("categoryName")} withAsterisk data-autofocus {...form.getInputProps("name")} />
        {editing && (
          <Switch
            label={t("categoryActive")}
            description={t("categoryActiveDescription")}
            {...form.getInputProps("active", { type: "checkbox" })}
          />
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
