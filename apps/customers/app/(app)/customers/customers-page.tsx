"use client";

import {
  ActionIcon,
  Button,
  Group,
  Menu,
  Select,
  SimpleGrid,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconArchive, IconDots, IconPencil, IconPlus, IconSearch } from "@tabler/icons-react";
import type { SortingState } from "@tanstack/react-table";
import type { Customer } from "@vantigo/customers/database/schema/customers";
import { DataTable, type DataTableColumn } from "@vantigo/customers/lib/components/data-table";
import { StatCard } from "@vantigo/customers/lib/components/stat-card";
import { useCustomerStats, useCustomers } from "@vantigo/customers/lib/customers/hooks";
import type { customerSortFields } from "@vantigo/customers/lib/customers/schemas";
import { countryFlag, countryName } from "@vantigo/customers/lib/i18n/country";
import { useRouter } from "next/navigation";
import { useLocale, useTranslations } from "next-intl";
import { useMemo, useState } from "react";
import {
  CreateCustomerModal,
  CustomerStatusBadge,
  EditCustomerModal,
  LegalTypeBadge,
  useConfirmArchive,
} from "./customer-modals";

type SortField = (typeof customerSortFields)[number];

export function CustomersPage({
  defaultLegalType,
}: Readonly<{ defaultLegalType?: Customer["legalType"] }>) {
  const t = useTranslations("customers");
  const locale = useLocale();
  const router = useRouter();

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [sorting, setSorting] = useState<SortingState>([{ id: "id", desc: false }]);
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, 250);
  const [legalType, setLegalType] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>("active");
  const [createOpened, setCreateOpened] = useState(false);
  const [editing, setEditing] = useState<Customer | null>(null);

  const query = {
    page,
    pageSize,
    sortBy: (sorting[0]?.id ?? "id") as SortField,
    sortDir: sorting[0]?.desc ? ("desc" as const) : ("asc" as const),
    search: debouncedSearch || undefined,
    legalType: (legalType as "private" | "business" | null) ?? undefined,
    status: (status as "active" | "archived" | null) ?? undefined,
  };

  const { data, isLoading } = useCustomers(query);
  const { data: stats } = useCustomerStats();
  const confirmArchive = useConfirmArchive();

  const columns = useMemo<DataTableColumn<Customer>[]>(
    () => [
      {
        id: "id",
        accessorKey: "id",
        header: t("table.id"),
        cell: ({ getValue }) => (
          <Text size="sm" fw={500}>
            #{getValue<number>()}
          </Text>
        ),
      },
      { id: "legalName", accessorKey: "legalName", header: t("table.legalName") },
      {
        id: "legalType",
        accessorKey: "legalType",
        header: t("table.legalType"),
        cell: ({ row }) => <LegalTypeBadge legalType={row.original.legalType} />,
      },
      {
        id: "legalId",
        accessorKey: "legalId",
        header: t("table.legalId"),
        enableSorting: false,
        cell: ({ row }) => (
          <Text size="sm" title={countryName(row.original.legalCountry, locale)}>
            {countryFlag(row.original.legalCountry)} {row.original.legalId}
          </Text>
        ),
      },
      { id: "email", accessorKey: "email", header: t("table.email") },
      {
        id: "phone",
        accessorKey: "phone",
        header: t("table.phone"),
        enableSorting: false,
      },
      {
        id: "status",
        accessorKey: "status",
        header: t("table.status"),
        cell: ({ row }) => <CustomerStatusBadge status={row.original.status} />,
      },
      {
        id: "actions",
        header: "",
        enableSorting: false,
        cell: ({ row }) => (
          <Menu withinPortal position="bottom-end">
            <Menu.Target>
              <ActionIcon
                variant="subtle"
                color="gray"
                onClick={(event) => event.stopPropagation()}
                aria-label={t("table.actions")}
              >
                <IconDots size={16} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown onClick={(event) => event.stopPropagation()}>
              <Menu.Item
                leftSection={<IconPencil size={14} />}
                onClick={() => setEditing(row.original)}
              >
                {t("table.quickEdit")}
              </Menu.Item>
              <Menu.Item
                color="red"
                leftSection={<IconArchive size={14} />}
                disabled={row.original.status === "archived"}
                onClick={() => confirmArchive(row.original)}
              >
                {t("table.archive")}
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        ),
      },
    ],
    // eslint-style exhaustive deps aren't in play; t is stable per locale
    [t, locale],
  );

  return (
    <>
      <Group justify="space-between" mb="lg">
        <Title order={2}>{t("title")}</Title>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setCreateOpened(true)}>
          {t("newCustomer")}
        </Button>
      </Group>

      <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} mb="lg">
        <StatCard label={t("stats.total")} value={String(stats?.total ?? "–")} />
        <StatCard label={t("stats.newThisMonth")} value={String(stats?.newThisMonth ?? "–")} />
        <StatCard label={t("stats.business")} value={String(stats?.businessCount ?? "–")} />
        <StatCard label={t("stats.private")} value={String(stats?.privateCount ?? "–")} />
      </SimpleGrid>

      <Group mb="md" gap="sm">
        <TextInput
          placeholder={t("searchPlaceholder")}
          leftSection={<IconSearch size={16} />}
          value={search}
          onChange={(event) => {
            setSearch(event.currentTarget.value);
            setPage(1);
          }}
          w={{ base: "100%", sm: 260 }}
        />
        <Select
          placeholder={t("filterLegalType")}
          data={[
            { value: "business", label: t("legalType.business") },
            { value: "private", label: t("legalType.private") },
          ]}
          value={legalType}
          onChange={(value) => {
            setLegalType(value);
            setPage(1);
          }}
          clearable
          w={160}
        />
        <Select
          placeholder={t("filterStatus")}
          data={[
            { value: "active", label: t("status.active") },
            { value: "archived", label: t("status.archived") },
          ]}
          value={status}
          onChange={(value) => {
            setStatus(value);
            setPage(1);
          }}
          clearable
          w={160}
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
        onPageChange={setPage}
        onPageSizeChange={(size) => {
          setPageSize(size);
          setPage(1);
        }}
        onSortingChange={setSorting}
        onRowClick={(customer) => router.push(`/customers/${customer.id}`)}
      />

      <CreateCustomerModal
        opened={createOpened}
        onClose={() => setCreateOpened(false)}
        defaultLegalType={defaultLegalType}
      />
      <EditCustomerModal customer={editing} onClose={() => setEditing(null)} />
    </>
  );
}
