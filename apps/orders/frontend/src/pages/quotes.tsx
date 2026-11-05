import { Alert, Button, Chip, Group, Pagination, Select, Stack, Switch, Text, TextInput } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { PAGE_SIZE } from "../api/list-query";
import { type QuoteListFilters, quoteListQueryOptions } from "../api/quotes";
import { salespeopleQueryOptions } from "../api/salespeople";
import type { QuoteStatus } from "../api/types";
import { CustomerPicker } from "../components/customer-picker";
import { QuoteTable } from "../components/document-tables";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { newQuoteLinkOptions } from "../lib/routes";

const statuses: QuoteStatus[] = ["draft", "sent", "accepted", "declined", "expired", "cancelled"];

/**
 * The quotes list (O20, O31a): newest first, filtered by state, customer,
 * the caller's own and a search, a page at a time. *New quote* is offered
 * with orders:quotes-manage.
 */
export const QuotesPage = ({ access = noOrdersAccess }: { access?: OrdersAccess }) => {
  const { t } = useOrdersFormat();
  const [filters, setFilters] = useState<QuoteListFilters>({ page: 1 });
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 300);
  const salespeople = useQuery(salespeopleQueryOptions());
  const list = useQuery(quoteListQueryOptions({ ...filters, q: debounced.trim() || undefined, pageSize: PAGE_SIZE }));
  const set = (next: Partial<QuoteListFilters>) =>
    setFilters((current) => ({ ...current, ...next, page: next.page ?? 1 }));
  const pages = list.data ? Math.max(1, Math.ceil(list.data.total / PAGE_SIZE)) : 1;
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("quotesTitle")}
        description={t("quotesDescription")}
        actions={
          access.canManageQuotes && (
            <Button
              leftSection={<IconPlus size={16} />}
              renderRoot={(props) => <Link {...newQuoteLinkOptions()} {...props} />}
              data-assist="orders.quotes.new"
            >
              {t("newQuote")}
            </Button>
          )
        }
      />
      <PageSection>
        <Stack gap="md">
          <Stack gap="sm">
            <Chip.Group
              multiple
              value={filters.status ?? []}
              onChange={(value) => set({ status: value as QuoteStatus[] })}
            >
              <Group gap={6} aria-label={t("status")} role="group">
                {statuses.map((status) => (
                  <Chip key={status} value={status} size="xs" variant="outline">
                    {t(`quoteStatus.${status}`)}
                  </Chip>
                ))}
              </Group>
            </Chip.Group>
            <Group align="flex-end" wrap="wrap">
              <TextInput
                label={t("search")}
                placeholder={t("searchQuotesPlaceholder")}
                leftSection={<IconSearch size={14} />}
                value={search}
                onChange={(e) => {
                  setSearch(e.currentTarget.value);
                  set({});
                }}
              />
              {access.canViewCustomers && (
                <CustomerPicker
                  label={t("customerFilter")}
                  anyStatus
                  value={filters.customerId ?? null}
                  onChange={(id) => set({ customerId: id ?? undefined })}
                />
              )}
              <Select
                label={t("salesperson")}
                placeholder={t("anySalesperson")}
                clearable
                searchable
                data={(salespeople.data ?? []).map((p) => ({ value: p.id, label: p.displayName }))}
                value={filters.salespersonId ?? null}
                onChange={(value) => set({ salespersonId: value ?? undefined })}
              />
              {access.userId && (
                <Switch
                  label={t("onlyMine")}
                  mb={8}
                  checked={filters.salespersonId === access.userId}
                  onChange={(e) => set({ salespersonId: e.currentTarget.checked ? access.userId : undefined })}
                />
              )}
            </Group>
          </Stack>
          {list.isError && (
            <Alert color="red" title={t("failedToLoadQuotes")}>
              {refusalMessage(list.error, t)}
            </Alert>
          )}
          {list.isPending && <ContentSkeleton rows={5} rowHeight={40} />}
          {list.data && list.data.items.length === 0 && (
            <EmptyState title={t("noQuotes")} description={t("noQuotesDescription")} />
          )}
          {list.data && list.data.items.length > 0 && (
            <>
              <QuoteTable rows={list.data.items} />
              <Group justify="space-between">
                <Text size="sm" c="dimmed">
                  {t("quoteCount", { count: list.data.total })}
                </Text>
                {pages > 1 && <Pagination total={pages} value={filters.page ?? 1} onChange={(page) => set({ page })} />}
              </Group>
            </>
          )}
        </Stack>
      </PageSection>
    </Stack>
  );
};
