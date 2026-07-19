"use client";

import {
  Center,
  Group,
  Loader,
  Pagination,
  Select,
  Table,
  Text,
  UnstyledButton,
} from "@mantine/core";
import { IconArrowDown, IconArrowUp, IconSelector } from "@tabler/icons-react";
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  type SortingState,
  useReactTable,
} from "@tanstack/react-table";

const PAGE_SIZE_OPTIONS = ["25", "50", "100"];

// biome-ignore lint/suspicious/noExplicitAny: column value types are heterogeneous by design
export type DataTableColumn<TData> = ColumnDef<TData, any>;

export interface DataTableProps<TData> {
  columns: DataTableColumn<TData>[];
  data: TData[];
  /** Total row count on the server (drives pagination). */
  total: number;
  page: number;
  pageSize: number;
  sorting: SortingState;
  isLoading?: boolean;
  emptyLabel: string;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
  onSortingChange: (sorting: SortingState) => void;
  onRowClick?: (row: TData) => void;
}

/**
 * Reusable server-driven data table: TanStack Table (headless) rendered with
 * Mantine components. Pagination, sorting and data loading are controlled by
 * the caller, which typically forwards them as API query parameters.
 */
export function DataTable<TData>({
  columns,
  data,
  total,
  page,
  pageSize,
  sorting,
  isLoading,
  emptyLabel,
  onPageChange,
  onPageSizeChange,
  onSortingChange,
  onRowClick,
}: DataTableProps<TData>) {
  const table = useReactTable({
    data,
    columns,
    state: { sorting },
    manualSorting: true,
    manualPagination: true,
    enableSortingRemoval: false,
    onSortingChange: (updater) => {
      onSortingChange(typeof updater === "function" ? updater(sorting) : updater);
    },
    getCoreRowModel: getCoreRowModel(),
  });

  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  return (
    <>
      <Table.ScrollContainer minWidth={700}>
        <Table highlightOnHover verticalSpacing="sm">
          <Table.Thead>
            {table.getHeaderGroups().map((headerGroup) => (
              <Table.Tr key={headerGroup.id}>
                {headerGroup.headers.map((header) => {
                  const canSort = header.column.getCanSort();
                  const sortDir = header.column.getIsSorted();
                  const SortIcon =
                    sortDir === "asc"
                      ? IconArrowUp
                      : sortDir === "desc"
                        ? IconArrowDown
                        : IconSelector;
                  return (
                    <Table.Th key={header.id}>
                      {canSort ? (
                        <UnstyledButton
                          onClick={header.column.getToggleSortingHandler()}
                          aria-label={`sort-${header.column.id}`}
                        >
                          <Group gap={4} wrap="nowrap">
                            <Text size="sm" fw={600}>
                              {flexRender(header.column.columnDef.header, header.getContext())}
                            </Text>
                            <SortIcon size={14} stroke={1.5} />
                          </Group>
                        </UnstyledButton>
                      ) : (
                        <Text size="sm" fw={600}>
                          {flexRender(header.column.columnDef.header, header.getContext())}
                        </Text>
                      )}
                    </Table.Th>
                  );
                })}
              </Table.Tr>
            ))}
          </Table.Thead>
          <Table.Tbody>
            {table.getRowModel().rows.map((row) => (
              <Table.Tr
                key={row.id}
                onClick={onRowClick ? () => onRowClick(row.original) : undefined}
                style={onRowClick ? { cursor: "pointer" } : undefined}
              >
                {row.getVisibleCells().map((cell) => (
                  <Table.Td key={cell.id}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </Table.Td>
                ))}
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>

      {isLoading && data.length === 0 && (
        <Center py="xl">
          <Loader size="sm" />
        </Center>
      )}
      {!isLoading && data.length === 0 && (
        <Center py="xl">
          <Text c="dimmed">{emptyLabel}</Text>
        </Center>
      )}

      <Group justify="space-between" mt="md">
        <Select
          size="xs"
          w={80}
          data={PAGE_SIZE_OPTIONS}
          value={String(pageSize)}
          onChange={(value) => value && onPageSizeChange(Number(value))}
          allowDeselect={false}
          aria-label="page-size"
        />
        <Pagination total={totalPages} value={page} onChange={onPageChange} size="sm" />
      </Group>
    </>
  );
}
