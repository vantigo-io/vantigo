import { Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { ApiValidationError, type CustomerResponse, createCustomer, updateCustomer } from "../api/customers";

export type CustomerModalState = { mode: "create" } | { mode: "edit"; customer: CustomerResponse };

export const CustomerFormModal = ({ state, onClose }: { state: CustomerModalState | null; onClose: () => void }) => {
  const queryClient = useQueryClient();
  const isEdit = state?.mode === "edit";
  const form = useForm({
    initialValues: { name: "" },
    validate: { name: (value: string) => (value.trim() ? null : "Name is required") },
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
        title: isEdit ? "Customer updated" : "Customer created",
        message: "The customer was saved successfully.",
      });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
      else notifications.show({ color: "red", title: "Customer could not be saved", message: error.message });
    },
  });
  return (
    <Modal opened={state !== null} onClose={onClose} title={isEdit ? "Edit customer" : "Create new customer"} centered>
      <form onSubmit={form.onSubmit(({ name }) => mutation.mutate(name.trim()))}>
        <Stack>
          <TextInput
            label="Name"
            description="A friendly name used to identify the customer"
            placeholder="e.g. Acme"
            withAsterisk
            data-autofocus
            {...form.getInputProps("name")}
          />
          <Text size="sm" c="dimmed">
            Legal identity is managed separately by users with the required permission.
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {isEdit ? "Save changes" : "Create customer"}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
