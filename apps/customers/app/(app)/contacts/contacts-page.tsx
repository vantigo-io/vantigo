"use client";

import { Badge, Button, Group, Modal, Text, TextInput, Title } from "@mantine/core";
import { useDebouncedValue, useDisclosure } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconPlus, IconSearch } from "@tabler/icons-react";
import type { SortingState } from "@tanstack/react-table";
import { DataTable, type DataTableColumn } from "@vantigo/customers/lib/components/data-table";
import { ContactForm } from "@vantigo/customers/lib/contacts/contact-form";
import { useContacts, useCreateContact } from "@vantigo/customers/lib/contacts/hooks";
import type { ContactListItem } from "@vantigo/customers/lib/contacts/queries";
import type { contactSortFields } from "@vantigo/customers/lib/contacts/schemas";
import { useRouter } from "next/navigation";
import { useFormatter, useTranslations } from "next-intl";
import { useMemo, useState } from "react";

type SortField = (typeof contactSortFields)[number];

export function ContactsPage() {
  const t = useTranslations("contacts");
  const format = useFormatter();
  const router = useRouter();

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [sorting, setSorting] = useState<SortingState>([{ id: "name", desc: false }]);
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, 250);
  const [createOpened, create] = useDisclosure(false);

  const { data, isLoading } = useContacts({
    page,
    pageSize,
    sortBy: (sorting[0]?.id ?? "name") as SortField,
    sortDir: sorting[0]?.desc ? "desc" : "asc",
    search: debouncedSearch || undefined,
  });

  const createContact = useCreateContact();

  const handleCreate = (input: Parameters<typeof createContact.mutate>[0]) => {
    createContact.mutate(input, {
      onSuccess: ({ contact }) => {
        notifications.show({
          color: "green",
          message: t("notifications.created", { name: contact.name }),
        });
        create.close();
      },
      onError: (error) => notifications.show({ color: "red", message: error.message }),
    });
  };

  const columns = useMemo<DataTableColumn<ContactListItem>[]>(
    () => [
      { id: "name", accessorKey: "name", header: t("table.name") },
      { id: "email", accessorKey: "email", header: t("table.email") },
      { id: "phone", accessorKey: "phone", header: t("table.phone"), enableSorting: false },
      {
        id: "customerCount",
        accessorKey: "customerCount",
        header: t("table.customers"),
        enableSorting: false,
        cell: ({ row }) => (
          <Badge variant="light" color={row.original.customerCount > 0 ? "blue" : "gray"}>
            {row.original.customerCount}
          </Badge>
        ),
      },
      {
        id: "createdAt",
        accessorKey: "createdAt",
        header: t("table.createdAt"),
        cell: ({ row }) => (
          <Text size="sm">
            {format.dateTime(new Date(row.original.createdAt), { dateStyle: "medium" })}
          </Text>
        ),
      },
    ],
    [t, format],
  );

  return (
    <>
      <Group justify="space-between" mb="lg">
        <Title order={2}>{t("title")}</Title>
        <Button leftSection={<IconPlus size={16} />} onClick={create.open}>
          {t("newContact")}
        </Button>
      </Group>

      <Group mb="md">
        <TextInput
          placeholder={t("searchPlaceholder")}
          leftSection={<IconSearch size={16} />}
          value={search}
          onChange={(event) => {
            setSearch(event.currentTarget.value);
            setPage(1);
          }}
          w={{ base: "100%", sm: 300 }}
        />
      </Group>

      <DataTable
        columns={columns}
        data={data?.items ?? []}
        total={data?.total ?? 0}
        page={page}
        pageSize={pageSize}
        sorting={sorting}
        isLoading={isLoading}
        emptyLabel={t("empty")}
        getRowId={(contact) => String(contact.id)}
        onPageChange={setPage}
        onPageSizeChange={(size) => {
          setPageSize(size);
          setPage(1);
        }}
        onSortingChange={setSorting}
        onRowClick={(contact) => router.push(`/contacts/${contact.id}`)}
      />

      <Modal opened={createOpened} onClose={create.close} title={t("createTitle")} size="lg">
        <ContactForm
          isSubmitting={createContact.isPending}
          submitLabel={t("form.create")}
          onSubmit={handleCreate}
        />
      </Modal>
    </>
  );
}
