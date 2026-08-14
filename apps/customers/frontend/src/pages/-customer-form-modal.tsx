import { Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import { ApiValidationError, type CustomerResponse, createCustomer, updateCustomer } from "../api/customers";
import "../i18n";

export type CustomerModalState = { mode: "create" } | { mode: "edit"; customer: CustomerResponse };

export const CustomerFormModal = ({ state, onClose }: { state: CustomerModalState | null; onClose: () => void }) => {
  const queryClient = useQueryClient();
  const { t } = useI18n("customers");
  const isEdit = state?.mode === "edit";
  const form = useForm({
    initialValues: { name: "" },
    validate: { name: (value: string) => (value.trim() ? null : t("customerNameRequired")) },
  });
  useEffect(() => {
    if (state) {
      form.setValues({ name: state.mode === "edit" ? state.customer.name : "" });
      form.resetDirty();
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);
  const mutation = useMutation({
    mutationFn: (name: string) => (isEdit ? updateCustomer(state.customer.id, { name }) : createCustomer({ name })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      onClose();
      notifications.show({
        color: "teal",
        title: isEdit ? t("customerUpdated") : t("customerCreated"),
        message: t("customerSaved"),
      });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
      else notifications.show({ color: "red", title: t("customerCouldNotBeSaved"), message: error.message });
    },
  });
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={isEdit ? t("editCustomer") : t("createNewCustomer")}
      centered
    >
      <form onSubmit={form.onSubmit(({ name }) => mutation.mutate(name.trim()))}>
        <Stack>
          <TextInput
            label={t("name")}
            description={t("customerNameDescription")}
            placeholder={t("customerNamePlaceholder")}
            withAsterisk
            data-autofocus
            {...form.getInputProps("name")}
          />
          <Text size="sm" c="dimmed">
            {t("legalIdentityPermission")}
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {isEdit ? t("saveChanges") : t("createCustomer")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
