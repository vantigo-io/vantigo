import { Button, Group, Modal, Stack, Text, TextInput, Tooltip } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import { updateCustomerContact } from "../api/contacts";
import { ApiValidationError } from "../api/customers";
import { NoValue } from "../components/legal-badges";

/**
 * Shows the connection-specific value when set, otherwise falls back to the
 * contact's own value (dimmed, to signal it is inherited).
 */
export const ConnectionValue = ({ own, connection }: { own: string | null; connection: string | null }) => {
  if (connection) {
    return <Text size="sm">{connection}</Text>;
  }

  if (own) {
    return (
      <Tooltip label="The contact's own value — no connection-specific one is set">
        <Text size="sm" c="dimmed">
          {own}
        </Text>
      </Tooltip>
    );
  }

  return <NoValue />;
};

export interface ConnectionFormValues {
  role: string;
  phone: string;
  email: string;
}

export const ConnectionFields = ({ getInputProps }: { getInputProps: (path: string) => object }) => (
  <>
    <TextInput label="Role" placeholder="e.g. CEO" withAsterisk {...getInputProps("role")} />
    <Group grow>
      <TextInput label="Phone" description="Specific to this customer connection" {...getInputProps("phone")} />
      <TextInput label="Email" description="Specific to this customer connection" {...getInputProps("email")} />
    </Group>
  </>
);

/** Identifies the association being edited plus its current values and modal title. */
export interface EditConnectionTarget {
  customerId: number;
  contactId: number;
  /** The name of the counterpart shown in the modal title. */
  counterpartName: string;
  role: string;
  phone: string | null;
  email: string | null;
}

interface EditConnectionModalProps {
  target: EditConnectionTarget | null;
  onClose: () => void;
}

/**
 * Edits the role and connection-specific contact details of a customer-contact
 * association. Shared between the customer dashboard (editing a contact's
 * connection) and the contact dashboard (editing a customer's connection).
 */
export const EditConnectionModal = ({ target, onClose }: EditConnectionModalProps) => {
  const queryClient = useQueryClient();

  const form = useForm<ConnectionFormValues>({
    initialValues: { role: "", phone: "", email: "" },
    validate: {
      role: (value) => (value.trim().length === 0 ? "Role is required" : null),
    },
  });

  // Sync form values when the modal opens for a different association.
  // The form object is recreated each render but its methods are stable, so it is
  // intentionally excluded from the dependency array.
  useEffect(() => {
    if (target) {
      form.setValues({
        role: target.role,
        phone: target.phone ?? "",
        email: target.email ?? "",
      });
      form.resetDirty();
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target]);

  const mutation = useMutation({
    mutationFn: (values: ConnectionFormValues) => {
      if (!target) {
        throw new Error("No association is being edited");
      }

      return updateCustomerContact(target.customerId, target.contactId, {
        role: values.role.trim(),
        phone: values.phone.trim() || undefined,
        email: values.email.trim() || undefined,
      });
    },
    onSuccess: () => {
      notifications.show({
        color: "teal",
        title: "Connection updated",
        message: target ? `The connection to "${target.counterpartName}" was updated.` : "",
      });
      if (target) {
        queryClient.invalidateQueries({ queryKey: ["customers", target.customerId, "contacts"] });
        queryClient.invalidateQueries({ queryKey: ["contacts", target.contactId, "customers"] });
      }
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({ color: "red", title: "Failed to update connection", message: error.message });
    },
  });

  return (
    <Modal
      opened={target !== null}
      onClose={onClose}
      title={target ? `Edit connection — ${target.counterpartName}` : ""}
      centered
    >
      <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
        <Stack>
          <ConnectionFields getInputProps={form.getInputProps} />
          <Group justify="flex-end" mt="xs">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              Save changes
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
