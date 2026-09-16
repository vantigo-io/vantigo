import {
  ActionIcon,
  Anchor,
  Breadcrumbs,
  Button,
  Card,
  Center,
  Combobox,
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
import { IconBuildingStore, IconMail, IconPencil, IconPhone, IconPlus, IconUserOff } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";

import {
  attachCustomerContact,
  type ContactCustomerResponse,
  type ContactResponse,
  contactCustomersQueryOptions,
  contactQueryOptions,
  detachCustomerContact,
} from "../api/contacts";
import { ApiValidationError, type CustomerResponse, customersQueryOptions } from "../api/customers";
import { CopyableBadge } from "../components/legal-badges";
import { formatContactName } from "../lib/format-contact-name";
import { ConnectionFields, ConnectionValue, EditConnectionModal, type EditConnectionTarget } from "./-connection";
import { ContactFormModal, type ContactModalState } from "./-contact-form-modal";
import "../i18n";

export const ContactDetailsPage = () => {
  const { t } = useI18n("customers");
  const { contactId } = useParams({ strict: false }) as { contactId: number };
  const { data: contact } = useSuspenseQuery(contactQueryOptions(contactId));
  const [modalState, setModalState] = useState<ContactModalState | null>(null);

  const name = formatContactName(contact);

  return (
    <Stack gap="lg">
      <Breadcrumbs>
        <Anchor component={Link} to={"/customers/contacts" as never} size="sm">
          {t("contacts")}
        </Anchor>
        <Text size="sm">{name}</Text>
      </Breadcrumbs>

      <Stack gap="xs">
        <PageHeader
          eyebrow={t("customers")}
          title={name}
          description={t("contactDetailsDescription")}
          actions={
            <Group gap="sm">
              <CopyableBadge variant="light" size="lg" tooltip={t("contactIdTooltip")} copyValue={String(contact.id)}>
                #{contact.id}
              </CopyableBadge>
              <Button
                variant="default"
                leftSection={<IconPencil size={16} />}
                onClick={() => setModalState({ mode: "edit", contact })}
              >
                {t("editContact")}
              </Button>
            </Group>
          }
        />

        {(contact.phone || contact.email) && (
          <Group gap="xs">
            {contact.phone && (
              <CopyableBadge
                variant="light"
                color="gray"
                radius="sm"
                tt="none"
                fw={500}
                leftSection={<IconPhone size={12} />}
                tooltip={t("contactPhoneTooltip")}
                copyValue={contact.phone}
              >
                {contact.phone}
              </CopyableBadge>
            )}
            {contact.email && (
              <CopyableBadge
                variant="light"
                color="gray"
                radius="sm"
                tt="none"
                fw={500}
                leftSection={<IconMail size={12} />}
                tooltip={t("contactEmailTooltip")}
                copyValue={contact.email}
              >
                {contact.email}
              </CopyableBadge>
            )}
          </Group>
        )}
      </Stack>

      <ContactFormModal state={modalState} onClose={() => setModalState(null)} />

      <ContactCustomersCard contact={contact} contactName={name} />
    </Stack>
  );
};

/**
 * The customers widget on the contact dashboard: a mini table of the customers the
 * contact is associated with and its role there, with actions to add, edit and
 * remove associations — the mirror of the customer dashboard's contacts card.
 */
const ContactCustomersCard = ({ contact, contactName }: { contact: ContactResponse; contactName: string }) => {
  const { t } = useI18n("customers");
  const contactId = contact.id;
  const navigate = useNavigate() as (options: unknown) => void;
  const queryClient = useQueryClient();
  const { data, isPending } = useQuery(contactCustomersQueryOptions(contactId));

  const [addModalOpened, setAddModalOpened] = useState(false);
  const [editing, setEditing] = useState<EditConnectionTarget | null>(null);

  const associations = data?.data ?? [];

  const detach = useMutation({
    mutationFn: (association: ContactCustomerResponse) => detachCustomerContact(association.customer.id, contactId),
    onSuccess: (_, association) => {
      notifications.show({
        color: "teal",
        title: t("customerRemoved"),
        message: t("customerRemovedMessage", { contact: contactName, customer: association.customer.name }),
      });
      queryClient.invalidateQueries({ queryKey: ["contacts"] });
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      queryClient.invalidateQueries({ queryKey: ["customers", association.customer.id, "timeline"] });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: t("failedRemoveCustomer"), message: error.message });
    },
  });

  const confirmDetach = (association: ContactCustomerResponse) =>
    modals.openConfirmModal({
      title: t("removeCustomer"),
      children: (
        <Text size="sm">
          {t("removeAssociationQuestion", { contact: contactName, customer: association.customer.name })}
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
            <IconBuildingStore size={18} stroke={1.5} />
            <Text fw={600}>{t("customers")}</Text>
          </Group>
          <Button
            variant="light"
            size="xs"
            leftSection={<IconPlus size={14} />}
            onClick={() => setAddModalOpened(true)}
          >
            {t("addCustomer")}
          </Button>
        </Group>

        {isPending ? (
          <Center py="md">
            <Loader size="sm" />
          </Center>
        ) : associations.length === 0 ? (
          <Center py="md">
            <Text size="sm" c="dimmed">
              {t("noCustomersAssociated")}
            </Text>
          </Center>
        ) : (
          <Table.ScrollContainer minWidth={480}>
            <Table highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("customer")}</Table.Th>
                  <Table.Th>{t("role")}</Table.Th>
                  <Table.Th>{t("email")}</Table.Th>
                  <Table.Th>{t("phone")}</Table.Th>
                  <Table.Th w={80} aria-label={t("actions")} />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {associations.map((association) => (
                  <Table.Tr
                    key={association.customer.id}
                    style={{ cursor: "pointer" }}
                    onClick={() =>
                      navigate({
                        href: `/customers/${association.customer.id}`,
                      })
                    }
                  >
                    <Table.Td onClick={(event) => event.stopPropagation()}>
                      <Anchor
                        size="sm"
                        renderRoot={(props) => (
                          <Link to={`/customers/${association.customer.id}` as never} {...props} />
                        )}
                      >
                        {association.customer.name}
                      </Anchor>
                    </Table.Td>
                    <Table.Td>{association.role}</Table.Td>
                    <Table.Td>
                      <ConnectionValue own={contact.email} connection={association.email} />
                    </Table.Td>
                    <Table.Td>
                      <ConnectionValue own={contact.phone} connection={association.phone} />
                    </Table.Td>
                    <Table.Td onClick={(event) => event.stopPropagation()}>
                      <Group gap={4} wrap="nowrap">
                        <ActionIcon
                          variant="subtle"
                          color="gray"
                          aria-label={t("editConnectionFor", { name: association.customer.name })}
                          onClick={() =>
                            setEditing({
                              customerId: association.customer.id,
                              contactId,
                              counterpartName: association.customer.name,
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
                          aria-label={t("removeNamed", { name: association.customer.name })}
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

      <AddCustomerModal
        contactId={contactId}
        contactName={contactName}
        attachedCustomerIds={associations.map((a) => a.customer.id)}
        opened={addModalOpened}
        onClose={() => setAddModalOpened(false)}
      />
      <EditConnectionModal target={editing} onClose={() => setEditing(null)} />
    </Card>
  );
};

interface AddCustomerModalProps {
  contactId: number;
  contactName: string;
  attachedCustomerIds: number[];
  opened: boolean;
  onClose: () => void;
}

/**
 * Associates the contact with an existing customer: search for the customer, then
 * give the connection a role and optional contact details.
 */
const AddCustomerModal = ({ contactId, contactName, attachedCustomerIds, opened, onClose }: AddCustomerModalProps) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const combobox = useCombobox();

  const [searchInput, setSearchInput] = useState("");
  const [debouncedSearch] = useDebouncedValue(searchInput, 300);
  const [selected, setSelected] = useState<CustomerResponse | null>(null);

  const lookup = useQuery({
    ...customersQueryOptions({ search: debouncedSearch, pageSize: 10 }),
    enabled: opened && debouncedSearch.trim().length >= 2 && !selected,
  });
  const suggestions = lookup.data?.data ?? [];

  const form = useForm({
    initialValues: { role: "", phone: "", email: "" },
    validate: {
      role: (value) => (value.trim().length === 0 ? t("roleRequired") : null),
    },
  });

  const reset = () => {
    setSearchInput("");
    setSelected(null);
    form.reset();
  };

  const close = () => {
    reset();
    onClose();
  };

  const mutation = useMutation({
    mutationFn: (customer: CustomerResponse) =>
      attachCustomerContact(customer.id, {
        contactId,
        role: form.values.role.trim(),
        phone: form.values.phone.trim() || undefined,
        email: form.values.email.trim() || undefined,
      }),
    onSuccess: (_, customer) => {
      notifications.show({
        color: "teal",
        title: t("customerAdded"),
        message: t("customerAddedMessage", { contact: contactName, customer: customer.name }),
      });
      queryClient.invalidateQueries({ queryKey: ["contacts"] });
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      queryClient.invalidateQueries({ queryKey: ["customers", customer.id, "timeline"] });
      close();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({ color: "red", title: t("customerCouldNotBeSaved"), message: error.message });
    },
  });

  const handleSubmit = form.onSubmit(() => {
    if (selected) {
      mutation.mutate(selected);
    }
  });

  return (
    <Modal opened={opened} onClose={close} title={t("addCustomer")} centered size="lg">
      <form onSubmit={handleSubmit}>
        <Stack>
          {!selected && (
            <Combobox
              store={combobox}
              onOptionSubmit={(value) => {
                const customer = suggestions.find((s) => String(s.id) === value);
                if (customer) {
                  setSelected(customer);
                }
                combobox.closeDropdown();
              }}
            >
              <Combobox.Target>
                <TextInput
                  label={t("searchForCustomer")}
                  description={t("searchCustomerDescription")}
                  placeholder={t("customerPlaceholder")}
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
                  {suggestions.map((customer) => {
                    const alreadyAttached = attachedCustomerIds.includes(customer.id);
                    return (
                      <Combobox.Option key={customer.id} value={String(customer.id)} disabled={alreadyAttached}>
                        <Group justify="space-between" wrap="nowrap">
                          <div>
                            <Text size="sm">{customer.name}</Text>
                            <Text size="xs" c="dimmed">
                              {t("customerDetails")}
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
                  {suggestions.length === 0 && <Combobox.Empty>{t("noCustomersFoundCombobox")}</Combobox.Empty>}
                </Combobox.Options>
              </Combobox.Dropdown>
            </Combobox>
          )}

          {selected && (
            <>
              <Group justify="space-between">
                <div>
                  <Text fw={500}>{selected.name}</Text>
                  <Text size="xs" c="dimmed">
                    {t("customer")}
                  </Text>
                </div>
                <Button variant="subtle" size="compact-sm" onClick={reset}>
                  {t("change")}
                </Button>
              </Group>

              <ConnectionFields getInputProps={form.getInputProps} />

              <Group justify="flex-end" mt="xs">
                <Button variant="default" onClick={close}>
                  {t("cancel")}
                </Button>
                <Button type="submit" loading={mutation.isPending}>
                  {t("addCustomer")}
                </Button>
              </Group>
            </>
          )}
        </Stack>
      </form>
    </Modal>
  );
};
