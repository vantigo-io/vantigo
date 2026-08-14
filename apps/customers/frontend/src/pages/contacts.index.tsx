import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Center,
  Group,
  Loader,
  Pagination,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconPlus, IconSearch, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";

import { type ContactListItem, contactsQueryOptions, deleteContact } from "../api/contacts";
import { NoValue } from "../components/legal-badges";
import { formatContactName } from "../lib/format-contact-name";
import { ContactFormModal, type ContactModalState } from "./-contact-form-modal";
import "../i18n";

const PAGE_SIZE = 25;

interface ContactsSearch {
  page: number;
  search: string;
}

export const ContactsPage = () => {
  const { t, formatters } = useI18n("customers");
  const { page, search } = useSearch({ strict: false }) as ContactsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const queryClient = useQueryClient();

  const [searchInput, setSearchInput] = useState(search);
  const [debouncedSearch] = useDebouncedValue(searchInput, 300);
  const [modalState, setModalState] = useState<ContactModalState | null>(null);

  useEffect(() => {
    if (debouncedSearch !== search) {
      navigate({
        search: { page: 1, search: debouncedSearch },
        replace: true,
      });
    }
  }, [debouncedSearch, search, navigate]);

  const { data, isPending, isError, error } = useQuery(
    contactsQueryOptions({
      page,
      pageSize: PAGE_SIZE,
      search: search || undefined,
    }),
  );

  const removal = useMutation({
    mutationFn: (item: ContactListItem) => deleteContact(item.contact.id),
    onSuccess: (_, item) => {
      notifications.show({
        color: "teal",
        title: t("contactDeleted"),
        message: `"${formatContactName(item.contact)}" was deleted.`,
      });
      queryClient.invalidateQueries({ queryKey: ["contacts"] });
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      const affectedCustomerIds = item.customer ? [item.customer.id] : [];
      for (const customerId of affectedCustomerIds) {
        queryClient.invalidateQueries({ queryKey: ["customers", customerId, "timeline"] });
      }
    },
    onError: (mutationError) => {
      notifications.show({
        color: "red",
        title: t("failedDeleteContact"),
        message: mutationError.message,
      });
    },
  });

  return (
    <Stack gap="lg">
      <PageHeader
        eyebrow={t("customers")}
        title={t("contacts")}
        description={t("contactsDescription")}
        actions={
          <Group gap="sm">
            {data && (
              <Badge variant="light" size="lg">
                {t("total", { count: formatters.formatNumber(data.pagination.totalCount) })}
              </Badge>
            )}
            <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
              {t("createNewContact")}
            </Button>
          </Group>
        }
      />

      <ContactFormModal state={modalState} onClose={() => setModalState(null)} />

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <TextInput
            placeholder={t("searchContacts")}
            leftSection={<IconSearch size={16} />}
            value={searchInput}
            onChange={(event) => setSearchInput(event.currentTarget.value)}
            maw={400}
          />

          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedLoadContacts")}>
              {error.message}
            </Alert>
          )}

          {isPending && (
            <Center py="xl">
              <Loader />
            </Center>
          )}

          {data && (
            <>
              <Table.ScrollContainer minWidth={640}>
                <Table striped highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("name")}</Table.Th>
                      <Table.Th>{t("phone")}</Table.Th>
                      <Table.Th>{t("email")}</Table.Th>
                      <Table.Th>{t("customerColumn")}</Table.Th>
                      <Table.Th w={80} aria-label={t("actions")} />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.data.map((item) => (
                      <Table.Tr
                        key={item.contact.id}
                        style={{ cursor: "pointer" }}
                        onClick={() =>
                          navigate({
                            to: "/contacts/$contactId",
                            params: { contactId: item.contact.id },
                          })
                        }
                      >
                        <Table.Td>{formatContactName(item.contact)}</Table.Td>
                        <Table.Td>{item.contact.phone ?? <NoValue />}</Table.Td>
                        <Table.Td>{item.contact.email ?? <NoValue />}</Table.Td>
                        <Table.Td onClick={(event) => event.stopPropagation()}>
                          <ContactCustomersCell item={item} />
                        </Table.Td>
                        <Table.Td onClick={(event) => event.stopPropagation()}>
                          <Group gap={4} wrap="nowrap">
                            <ActionIcon
                              variant="subtle"
                              color="gray"
                              aria-label={t("editNamed", { name: formatContactName(item.contact) })}
                              onClick={() => setModalState({ mode: "edit", contact: item.contact })}
                            >
                              <IconPencil size={16} />
                            </ActionIcon>
                            <ActionIcon
                              variant="subtle"
                              color="red"
                              aria-label={t("removeNamed", { name: formatContactName(item.contact) })}
                              loading={removal.isPending && removal.variables === item}
                              onClick={() => removal.mutate(item)}
                            >
                              <IconTrash size={16} />
                            </ActionIcon>
                          </Group>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>

              {data.data.length === 0 && (
                <Center py="xl">
                  <Text c="dimmed">{t("noContactsFound")}</Text>
                </Center>
              )}

              {data.pagination.totalPages > 1 && (
                <Group justify="center">
                  <Pagination
                    total={data.pagination.totalPages}
                    value={page}
                    onChange={(newPage) => navigate({ search: { page: newPage, search } })}
                  />
                </Group>
              )}
            </>
          )}
        </Stack>
      </Card>
    </Stack>
  );
};

/**
 * The customers column: the customer's name (linked) when the contact is associated
 * with exactly one, otherwise the number of associations.
 */
const ContactCustomersCell = ({ item }: { item: ContactListItem }) => {
  const customer = item.customer;

  if (customer) {
    return (
      <Anchor
        size="sm"
        renderRoot={(props) => <Link to="/customers/$customerId" params={{ customerId: customer.id }} {...props} />}
      >
        {customer.name}
      </Anchor>
    );
  }

  return (
    <Badge variant="light" color={item.customerCount === 0 ? "gray" : "blue"} radius="sm">
      {item.customerCount}
    </Badge>
  );
};
