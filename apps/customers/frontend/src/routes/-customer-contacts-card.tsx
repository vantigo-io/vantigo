import {
  ActionIcon,
  Button,
  Card,
  Center,
  Combobox,
  Divider,
  Group,
  Loader,
  Modal,
  Stack,
  Table,
  Text,
  TextInput,
  useCombobox,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { useDebouncedValue } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconPlus, IconUserOff, IconUsersGroup } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import {
  attachCustomerContact,
  type ContactResponse,
  type CustomerContactResponse,
  contactsQueryOptions,
  createContact,
  customerContactsQueryOptions,
  detachCustomerContact,
} from "../api/contacts";
import { ApiValidationError } from "../api/customers";
import { formatContactName } from "../lib/format-contact-name";
import { ConnectionFields, ConnectionValue, EditConnectionModal, type EditConnectionTarget } from "./-connection";
import { ContactFields, toContactInput } from "./-contact-form-modal";

/**
 * The contacts widget on the customer dashboard: a mini table of the contacts
 * associated with the customer and their role, with actions to add, edit and
 * remove associations.
 */
export const CustomerContactsCard = ({ customerId }: { customerId: number }) => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data, isPending } = useQuery(customerContactsQueryOptions(customerId));

  const [addModalOpened, setAddModalOpened] = useState(false);
  const [editing, setEditing] = useState<EditConnectionTarget | null>(null);

  const associations = data?.data ?? [];

  const detach = useMutation({
    mutationFn: (association: CustomerContactResponse) => detachCustomerContact(customerId, association.contact.id),
    onSuccess: (_, association) => {
      notifications.show({
        color: "teal",
        title: "Contact removed",
        message: `"${formatContactName(association.contact)}" is no longer associated with this customer.`,
      });
      queryClient.invalidateQueries({ queryKey: ["customers", customerId, "contacts"] });
      queryClient.invalidateQueries({ queryKey: ["contacts"] });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: "Failed to remove contact", message: error.message });
    },
  });

  const confirmDetach = (association: CustomerContactResponse) =>
    modals.openConfirmModal({
      title: "Remove contact",
      children: (
        <Text size="sm">
          Remove "{formatContactName(association.contact)}" from this customer? The contact itself is kept and can be
          added again later.
        </Text>
      ),
      labels: { confirm: "Remove", cancel: "Cancel" },
      confirmProps: { color: "red" },
      onConfirm: () => detach.mutate(association),
    });

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between">
          <Group gap="xs">
            <IconUsersGroup size={18} stroke={1.5} />
            <Text fw={600}>Contacts</Text>
          </Group>
          <Button
            variant="light"
            size="xs"
            leftSection={<IconPlus size={14} />}
            onClick={() => setAddModalOpened(true)}
          >
            Add contact
          </Button>
        </Group>

        {isPending ? (
          <Center py="md">
            <Loader size="sm" />
          </Center>
        ) : associations.length === 0 ? (
          <Center py="md">
            <Text size="sm" c="dimmed">
              No contacts associated with this customer yet.
            </Text>
          </Center>
        ) : (
          <Table.ScrollContainer minWidth={480}>
            <Table highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Role</Table.Th>
                  <Table.Th>Email</Table.Th>
                  <Table.Th>Phone</Table.Th>
                  <Table.Th w={80} aria-label="Actions" />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {associations.map((association) => (
                  <Table.Tr
                    key={association.contact.id}
                    style={{ cursor: "pointer" }}
                    onClick={() =>
                      navigate({
                        to: "/contacts/$contactId",
                        params: { contactId: association.contact.id },
                      })
                    }
                  >
                    <Table.Td>{formatContactName(association.contact)}</Table.Td>
                    <Table.Td>{association.role}</Table.Td>
                    <Table.Td>
                      <ConnectionValue own={association.contact.email} connection={association.email} />
                    </Table.Td>
                    <Table.Td>
                      <ConnectionValue own={association.contact.phone} connection={association.phone} />
                    </Table.Td>
                    <Table.Td onClick={(event) => event.stopPropagation()}>
                      <Group gap={4} wrap="nowrap">
                        <ActionIcon
                          variant="subtle"
                          color="gray"
                          aria-label={`Edit connection for ${formatContactName(association.contact)}`}
                          onClick={() =>
                            setEditing({
                              customerId,
                              contactId: association.contact.id,
                              counterpartName: formatContactName(association.contact),
                              role: association.role,
                              phone: association.phone,
                              email: association.email,
                            })
                          }
                        >
                          <IconPencil size={16} />
                        </ActionIcon>
                        <ActionIcon
                          variant="subtle"
                          color="red"
                          aria-label={`Remove ${formatContactName(association.contact)}`}
                          onClick={() => confirmDetach(association)}
                        >
                          <IconUserOff size={16} />
                        </ActionIcon>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>

      <AddContactModal
        customerId={customerId}
        attachedContactIds={associations.map((a) => a.contact.id)}
        opened={addModalOpened}
        onClose={() => setAddModalOpened(false)}
      />
      <EditConnectionModal target={editing} onClose={() => setEditing(null)} />
    </Card>
  );
};

interface AddContactModalProps {
  customerId: number;
  attachedContactIds: number[];
  opened: boolean;
  onClose: () => void;
}

/**
 * The two-step "Add contact" flow: search for an existing contact and attach it with
 * a role, or — when nothing matches — create a new contact and attach it in one go.
 */
const AddContactModal = ({ customerId, attachedContactIds, opened, onClose }: AddContactModalProps) => {
  const queryClient = useQueryClient();
  const combobox = useCombobox();

  const [searchInput, setSearchInput] = useState("");
  const [debouncedSearch] = useDebouncedValue(searchInput, 300);
  const [selected, setSelected] = useState<ContactResponse | null>(null);
  const [creatingNew, setCreatingNew] = useState(false);

  const lookup = useQuery({
    ...contactsQueryOptions({ search: debouncedSearch, pageSize: 10 }),
    enabled: opened && debouncedSearch.trim().length >= 2 && !selected && !creatingNew,
  });
  const suggestions = lookup.data?.data ?? [];

  const form = useForm({
    initialValues: {
      firstName: "",
      lastName: "",
      middleName: "",
      prefix: "",
      suffix: "",
      phone: "",
      email: "",
      role: "",
      connectionPhone: "",
      connectionEmail: "",
    },
    validate: {
      role: (value) => (value.trim().length === 0 ? "Role is required" : null),
      firstName: (value) => (creatingNew && !selected && value.trim().length === 0 ? "First name is required" : null),
      lastName: (value) => (creatingNew && !selected && value.trim().length === 0 ? "Last name is required" : null),
    },
  });

  const reset = () => {
    setSearchInput("");
    setSelected(null);
    setCreatingNew(false);
    form.reset();
  };

  const close = () => {
    reset();
    onClose();
  };

  const onSuccess = (contact: ContactResponse) => {
    notifications.show({
      color: "teal",
      title: "Contact added",
      message: `"${formatContactName(contact)}" was added to this customer.`,
    });
    queryClient.invalidateQueries({ queryKey: ["customers", customerId, "contacts"] });
    queryClient.invalidateQueries({ queryKey: ["contacts"] });
    close();
  };

  const attachExisting = useMutation({
    mutationFn: (contact: ContactResponse) =>
      attachCustomerContact(customerId, {
        contactId: contact.id,
        role: form.values.role.trim(),
        phone: form.values.connectionPhone.trim() || undefined,
        email: form.values.connectionEmail.trim() || undefined,
      }),
    onSuccess: (association) => onSuccess(association.contact),
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(mapConnectionErrors(error));
        return;
      }
      notifications.show({ color: "red", title: "Failed to add contact", message: error.message });
    },
  });

  const createAndAttach = useMutation({
    mutationFn: async () => {
      const contact = await createContact(toContactInput(form.values));

      try {
        await attachCustomerContact(customerId, {
          contactId: contact.id,
          role: form.values.role.trim(),
        });
      } catch (error) {
        // The contact exists at this point — make that explicit so it is not
        // silently orphaned when only the association fails.
        throw new Error(
          `The contact "${formatContactName(contact)}" was created, but could not be added to the customer: ${
            error instanceof Error ? error.message : "unknown error"
          }`,
          { cause: error },
        );
      }

      return contact;
    },
    onSuccess,
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({ color: "red", title: "Failed to add contact", message: error.message });
    },
  });

  const handleSubmit = form.onSubmit(() => {
    if (selected) {
      attachExisting.mutate(selected);
    } else if (creatingNew) {
      createAndAttach.mutate();
    }
  });

  const showConnectionForm = selected !== null || creatingNew;

  return (
    <Modal opened={opened} onClose={close} title="Add contact" centered size="lg">
      <form onSubmit={handleSubmit}>
        <Stack>
          {!showConnectionForm && (
            <Combobox
              store={combobox}
              onOptionSubmit={(value) => {
                if (value === "create-new") {
                  setCreatingNew(true);
                  form.setFieldValue("firstName", searchInput.trim());
                } else {
                  const contact = suggestions.find((s) => String(s.contact.id) === value)?.contact;
                  if (contact) {
                    setSelected(contact);
                  }
                }
                combobox.closeDropdown();
              }}
            >
              <Combobox.Target>
                <TextInput
                  label="Search for a contact"
                  description="Search by name, phone or email"
                  placeholder="e.g. Anders"
                  data-autofocus
                  value={searchInput}
                  rightSection={lookup.isFetching && <Loader size="xs" />}
                  onChange={(event) => {
                    setSearchInput(event.currentTarget.value);
                    combobox.openDropdown();
                  }}
                  onFocus={() => combobox.openDropdown()}
                  onBlur={() => combobox.closeDropdown()}
                />
              </Combobox.Target>

              <Combobox.Dropdown hidden={debouncedSearch.trim().length < 2 || lookup.isPending}>
                <Combobox.Options>
                  {suggestions.map((item) => {
                    const alreadyAttached = attachedContactIds.includes(item.contact.id);
                    return (
                      <Combobox.Option key={item.contact.id} value={String(item.contact.id)} disabled={alreadyAttached}>
                        <Group justify="space-between" wrap="nowrap">
                          <div>
                            <Text size="sm">{formatContactName(item.contact)}</Text>
                            <Text size="xs" c="dimmed">
                              {[item.contact.email, item.contact.phone].filter(Boolean).join(" · ") ||
                                "No contact details"}
                            </Text>
                          </div>
                          {alreadyAttached && (
                            <Text size="xs" c="dimmed">
                              Already added
                            </Text>
                          )}
                        </Group>
                      </Combobox.Option>
                    );
                  })}
                  {suggestions.length === 0 && (
                    <Combobox.Option value="create-new">
                      <Text size="sm">No contact found — create "{searchInput.trim()}" as a new contact</Text>
                    </Combobox.Option>
                  )}
                </Combobox.Options>
              </Combobox.Dropdown>
            </Combobox>
          )}

          {selected && (
            <Group justify="space-between">
              <div>
                <Text fw={500}>{formatContactName(selected)}</Text>
                <Text size="xs" c="dimmed">
                  {[selected.email, selected.phone].filter(Boolean).join(" · ") || "No contact details"}
                </Text>
              </div>
              <Button variant="subtle" size="compact-sm" onClick={reset}>
                Change
              </Button>
            </Group>
          )}

          {creatingNew && (
            <>
              <Group justify="space-between">
                <Text fw={500}>New contact</Text>
                <Button variant="subtle" size="compact-sm" onClick={reset}>
                  Back to search
                </Button>
              </Group>
              <ContactFields
                getInputProps={form.getInputProps}
                setFieldValue={form.setFieldValue}
                values={form.values}
              />
              <Divider />
            </>
          )}

          {showConnectionForm && (
            <>
              {selected ? (
                <ConnectionFields getInputProps={(path) => form.getInputProps(mapConnectionPath(path))} />
              ) : (
                <TextInput label="Role" placeholder="e.g. CEO" withAsterisk {...form.getInputProps("role")} />
              )}
              <Group justify="flex-end" mt="xs">
                <Button variant="default" onClick={close}>
                  Cancel
                </Button>
                <Button type="submit" loading={attachExisting.isPending || createAndAttach.isPending}>
                  Add contact
                </Button>
              </Group>
            </>
          )}
        </Stack>
      </form>
    </Modal>
  );
};

/** The connection fields live under prefixed paths to avoid clashing with the contact's own. */
const mapConnectionPath = (path: string) =>
  path === "phone" ? "connectionPhone" : path === "email" ? "connectionEmail" : path;

/** Maps attach validation errors (keyed role/phone/email) onto the prefixed form paths. */
const mapConnectionErrors = (error: ApiValidationError) =>
  Object.fromEntries(Object.entries(error.fieldErrors).map(([field, message]) => [mapConnectionPath(field), message]));
