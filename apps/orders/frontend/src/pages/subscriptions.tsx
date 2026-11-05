import { Alert, Button, Chip, Group, Pagination, Stack, Text, TextInput } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { PAGE_SIZE } from "../api/list-query";
import { type SubscriptionListFilters, subscriptionListQueryOptions } from "../api/subscriptions";
import type { SubscriptionStatus } from "../api/types";
import { CustomerPicker } from "../components/customer-picker";
import { SubscriptionTable } from "../components/document-tables";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { newSubscriptionLinkOptions } from "../lib/routes";

const statuses: SubscriptionStatus[] = ["draft", "active", "paused", "ended", "cancelled"];

/**
 * The subscriptions list (O42): newest first, filtered by state, customer
 * and a search, each row with its plan, start and end, how far it is billed
 * and a full period's amount. *New subscription* is offered with
 * orders:subscriptions-manage.
 */
export const SubscriptionsPage = ({ access = noOrdersAccess }: { access?: OrdersAccess }) => {
  const { t } = useOrdersFormat();
  const [filters, setFilters] = useState<SubscriptionListFilters>({ page: 1 });
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 300);
  const list = useQuery(
    subscriptionListQueryOptions({ ...filters, q: debounced.trim() || undefined, pageSize: PAGE_SIZE }),
  );
  const set = (next: Partial<SubscriptionListFilters>) =>
    setFilters((current) => ({ ...current, ...next, page: next.page ?? 1 }));
  const pages = list.data ? Math.max(1, Math.ceil(list.data.total / PAGE_SIZE)) : 1;
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("subscriptionsTitle")}
        description={t("subscriptionsDescription")}
        actions={
          access.canManageSubscriptions && (
            <Button
              leftSection={<IconPlus size={16} />}
              renderRoot={(props) => <Link {...newSubscriptionLinkOptions()} {...props} />}
              data-assist="orders.subscriptions.new"
            >
              {t("newSubscription")}
            </Button>
          )
        }
      />
      <Group align="flex-end" wrap="wrap">
        <Chip.Group
          multiple
          value={filters.status ?? []}
          onChange={(value) => set({ status: value as SubscriptionStatus[] })}
        >
          <Group gap={6} aria-label={t("status")} role="group" mb={8}>
            {statuses.map((status) => (
              <Chip key={status} value={status} size="xs" variant="outline">
                {t(`subscriptionStatus.${status}`)}
              </Chip>
            ))}
          </Group>
        </Chip.Group>
        <TextInput
          label={t("search")}
          placeholder={t("searchSubscriptionsPlaceholder")}
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
      </Group>
      {list.isError && (
        <Alert color="red" title={t("failedToLoadSubscriptions")}>
          {refusalMessage(list.error, t)}
        </Alert>
      )}
      {list.isPending && <ContentSkeleton rows={5} rowHeight={40} />}
      {list.data && list.data.items.length === 0 && (
        <EmptyState title={t("noSubscriptions")} description={t("noSubscriptionsDescription")} />
      )}
      {list.data && list.data.items.length > 0 && (
        <>
          <SubscriptionTable rows={list.data.items} />
          <Group justify="space-between">
            <Text size="sm" c="dimmed">
              {t("subscriptionCount", { count: list.data.total })}
            </Text>
            {pages > 1 && <Pagination total={pages} value={filters.page ?? 1} onChange={(page) => set({ page })} />}
          </Group>
        </>
      )}
    </Stack>
  );
};
