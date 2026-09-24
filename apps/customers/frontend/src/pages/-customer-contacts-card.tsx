import {
  ActionIcon,
  Button,
  Card,
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
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
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
import { ContactRoleBadges } from "../components/contact-role-badges";
import { customerWriteErrorMessage } from "../lib/customer-write-error";
import { formatContactName } from "../lib/format-contact-name";
import {
  ConnectionFields,
  type ConnectionFormValues,
  ConnectionValue,
  EditConnectionModal,
  type EditConnectionTarget,
  toRoleInputs,
} from "./-connection";
import { ContactFields, toContactInput } from "./-contact-form-modal";
import "../i18n";

/**
 * The contacts widget on the customer dashboard: a mini table of the contacts
 * associated with the customer and their role, with actions to add, edit and
 * remove associations.
 */
export const CustomerContactsCard = ({
  customerId,
  readOnly = false,
}: {
  customerId: number;
  /** A merged-away customer's card (merge design D4): the list, with nothing to add, edit or remove. */
  readOnly?: boolean;
}) => {
  const { t } = useI18n("customers");
  const navigate = useNavigate() as (options: unknown) => void;
  const queryClient = useQueryClient();
  const { data, isPending } = useQuery(customerContactsQueryOptions(customerId));

  const [addModalOpened, setAddModalOpened] = useState(false);
  const [editing, setEditing] = useState<EditConnectionTarget | null>(null);

  const associations = data?.data ?? [];
  const invalidateCustomerTimeline = () =>
    queryClient.invalidateQueries({ queryKey: ["customers", customerId, "timeline"] });

  const detach = useMutation({
    mutationFn: (association: CustomerContactResponse) => detachCustomerContact(customerId, association.contact.id),
    onSuccess: (_, association) => {
      notifications.show({
        color: "teal",
        title: t("contactRemoved"),
        message: t("customerRemovedMessage", {
          contact: formatContactName(association.contact),
          customer: t("customer"),
        }),
      });
      queryClient.invalidateQueries({ queryKey: ["customers", customerId, "contacts"] });
      queryClient.invalidateQueries({ queryKey: ["contacts"] });
      invalidateCustomerTimeline();
    },
    onError: (error) => {
      notifications.show({
        color: "red",
        title: t("failedRemoveCustomer"),
        message: customerWriteErrorMessage(error, t),
      });
    },
  });

  const confirmDetach = (association: CustomerContactResponse) =>
    modals.openConfirmModal({
      title: t("removeNamed", { name: t("contact") }),
      children: (
        <Text size="sm">
          {t("removeAssociationQuestion", { contact: formatContactName(association.contact), customer: t("customer") })}
        </Text>
      ),
      labels: { confirm: t("remove"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => detach.mutate(association),
    });

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between">
          <Group gap="xs">
            <IconUsersGroup size={18} stroke={1.5} />
            <Text fw={600}>{t("contacts")}</Text>
          </Group>
          {!readOnly && (
            <Button
              variant="light"
              size="xs"
              leftSection={<IconPlus size={14} />}
              onClick={() => setAddModalOpened(true)}
            >
              {t("addContact")}
            </Button>
          )}
        </Group>

        {isPending ? (
          <ContentSkeleton rows={2} rowHeight={32} />
        ) : associations.length === 0 ? (
          <EmptyState size="sm" title={t("noContactsAssociated")} />
        ) : (
          <Table.ScrollContainer minWidth={480}>
            <Table highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("name")}</Table.Th>
                  <Table.Th>{t("contactRoles")}</Table.Th>
                  <Table.Th>{t("email")}</Table.Th>
                  <Table.Th>{t("phone")}</Table.Th>
                  <Table.Th w={80} aria-label={t("actions")} />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {associations.map((association) => (
                  <Table.Tr
                    key={association.contact.id}
                    style={{ cursor: "pointer" }}
                    onClick={() =>
                      navigate({
                        href: `/customers/contacts/${association.contact.id}`,
                      })
                    }
                  >
                    <Table.Td>
                      <Text size="sm">{formatContactName(association.contact)}</Text>
                      {association.title && (
                        <Text size="xs" c="dimmed">
                          {association.title}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <ContactRoleBadges roles={association.roles} />
                    </Table.Td>
                    <Table.Td>
                      <ConnectionValue own={association.contact.email} connection={association.email} />
                    </Table.Td>
                    <Table.Td>
                      <ConnectionValue own={association.contact.phone} connection={association.phone} />
                    </Table.Td>
                    <Table.Td onClick={(event) => event.stopPropagation()}>
                      {!readOnly && (
                        <Group gap={4} wrap="nowrap">
                          <ActionIcon
                            variant="subtle"
                            color="gray"
                            aria-label={t("editConnectionFor", { name: formatContactName(association.contact) })}
                            onClick={() =>
                              setEditing({
                                customerId,
                                contactId: association.contact.id,
                                counterpartName: formatContactName(association.contact),
                                title: association.title,
                                roles: association.roles,
                                soleRoles: association.roles
                                  .filter(
                                    (assignment) =>
                                      !associations.some(
                                        (other) =>
                                          other.contact.id !== association.contact.id &&
                                          other.roles.some((r) => r.role === assignment.role),
                                      ),
                                  )
                                  .map((assignment) => assignment.role),
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
                            aria-label={t("removeNamed", { name: formatContactName(association.contact) })}
                            onClick={() => confirmDetach(association)}
                          >
                            <IconUserOff size={16} />
                          </ActionIcon>
                        </Group>
                      )}
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
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const invalidateCustomerTimeline = () =>
    queryClient.invalidateQueries({ queryKey: ["customers", customerId, "timeline"] });
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
      title: "",
      roles: {} as Record<string, boolean>,
      primary: {} as Record<string, boolean>,
      connectionPhone: "",
      connectionEmail: "",
    },
    validate: {
      title: (value, values) =>
        value.trim().length === 0 && toRoleInputs(connectionValuesOf(values)).length === 0
          ? t("titleOrRoleRequired")
          : null,
      firstName: (value) => (creatingNew && !selected && value.trim().length === 0 ? t("firstNameRequired") : null),
      lastName: (value) => (creatingNew && !selected && value.trim().length === 0 ? t("lastNameRequired") : null),
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
      title: t("contactAdded"),
      message: t("contactSaved", { name: formatContactName(contact), action: t("addContact") }),
    });
    queryClient.invalidateQueries({ queryKey: ["customers", customerId, "contacts"] });
    queryClient.invalidateQueries({ queryKey: ["contacts"] });
    invalidateCustomerTimeline();
    close();
  };

  const attachExisting = useMutation({
    mutationFn: (contact: ContactResponse) =>
      attachCustomerContact(customerId, {
        contactId: contact.id,
        title: form.values.title.trim() || undefined,
        roles: toRoleInputs(connectionValuesOf(form.values)),
        phone: form.values.connectionPhone.trim() || undefined,
        email: form.values.connectionEmail.trim() || undefined,
      }),
    onSuccess: (association) => onSuccess(association.contact),
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(mapConnectionErrors(error));
        return;
      }
      notifications.show({
        color: "red",
        title: t("contactCouldNotBeCreated"),
        message: customerWriteErrorMessage(error, t),
      });
    },
  });

  const createAndAttach = useMutation({
    mutationFn: async () => {
      const contact = await createContact(toContactInput(form.values));

      try {
        await attachCustomerContact(customerId, {
          contactId: contact.id,
          title: form.values.title.trim() || undefined,
          roles: toRoleInputs(connectionValuesOf(form.values)),
          phone: form.values.connectionPhone.trim() || undefined,
          email: form.values.connectionEmail.trim() || undefined,
        });
      } catch (error) {
        if (error instanceof ApiValidationError) {
          // The contact exists at this point but the attach was refused for a
          // field reason — say so here, then rethrow marked as attach-scoped:
          // only a ConnectionValidationError's fields get routed through
          // mapConnectionErrors, because attach and create share field names
          // (title/roles/phone/email) that mean different things depending on
          // which step refused them — createContact's own `email`/`phone`
          // must land on the contact's own inputs, not the connection ones.
          notifications.show({
            color: "red",
            title: t("contactCouldNotBeCreated"),
            message: t("contactCreatedNotAttached", { name: formatContactName(contact) }),
          });
          throw new ConnectionValidationError(error);
        }
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
      if (error instanceof ConnectionValidationError) {
        form.setErrors(mapConnectionErrors(error));
        return;
      }
      if (error instanceof ApiValidationError) {
        // A create-step 400 — its `email`/`phone`/etc. name the contact's own
        // fields, not the connection's, so these land unmapped.
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({
        color: "red",
        title: t("contactCouldNotBeCreated"),
        message: customerWriteErrorMessage(error, t),
      });
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
    <Modal opened={opened} onClose={close} title={t("addContact")} centered size="lg">
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
                  label={t("searchForContact")}
                  description={t("searchContactDescription")}
                  placeholder={t("contactPlaceholder")}
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
                                t("noContactDetails")}
                            </Text>
                          </div>
                          {alreadyAttached && (
                            <Text size="xs" c="dimmed">
                              {t("alreadyAdded")}
                            </Text>
                          )}
                        </Group>
                      </Combobox.Option>
                    );
                  })}
                  {suggestions.length === 0 && (
                    <Combobox.Option value="create-new">
                      <Text size="sm">{t("noContactFoundCreate", { name: searchInput.trim() })}</Text>
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
                  {[selected.email, selected.phone].filter(Boolean).join(" · ") || t("noContactDetails")}
                </Text>
              </div>
              <Button variant="subtle" size="compact-sm" onClick={reset}>
                {t("change")}
              </Button>
            </Group>
          )}

          {creatingNew && (
            <>
              <Group justify="space-between">
                <Text fw={500}>{t("newContact")}</Text>
                <Button variant="subtle" size="compact-sm" onClick={reset}>
                  {t("backToSearch")}
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
              <ConnectionFields
                getInputProps={(path) => form.getInputProps(mapConnectionPath(path))}
                values={connectionValuesOf(form.values)}
                setFieldValue={(path, value) => form.setFieldValue(mapConnectionPath(path), value)}
                clearFieldError={(path) => form.clearFieldError(mapConnectionPath(path))}
                lockedPrimary={[]}
                soleRoles={[]}
              />
              <Group justify="flex-end" mt="xs">
                <Button variant="default" onClick={close}>
                  {t("cancel")}
                </Button>
                <Button type="submit" loading={attachExisting.isPending || createAndAttach.isPending}>
                  {t("addContact")}
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

/** Maps attach validation errors (keyed title/roles/phone/email) onto the prefixed form paths. */
const mapConnectionErrors = (error: ApiValidationError) =>
  Object.fromEntries(Object.entries(error.fieldErrors).map(([field, message]) => [mapConnectionPath(field), message]));

/**
 * Marks a 400 as coming from the attach step, not from creating the contact —
 * the two share field names (title/roles/phone/email) that mapConnectionErrors
 * must only ever apply to the attach's own.
 */
class ConnectionValidationError extends ApiValidationError {
  constructor(cause: ApiValidationError) {
    super(cause.message, cause.errors, cause.status);
    this.name = "ConnectionValidationError";
  }
}

/**
 * The attach form holds the contact's own fields beside the connection's, with
 * the two clashing ones prefixed; this is the connection half of it, in the
 * shape ConnectionFields and toRoleInputs speak.
 */
const connectionValuesOf = (values: {
  title: string;
  roles: Record<string, boolean>;
  primary: Record<string, boolean>;
  connectionPhone: string;
  connectionEmail: string;
}): ConnectionFormValues => ({
  title: values.title,
  roles: values.roles,
  primary: values.primary,
  phone: values.connectionPhone,
  email: values.connectionEmail,
});
