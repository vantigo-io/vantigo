import { Alert, Button, Card, Group, Pagination, Stack, Text } from "@mantine/core";
import { IconAlertCircle, IconPlus } from "@tabler/icons-react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState } from "@vantigo/frontend-shell";
import { useState } from "react";
import { invoiceListQueryOptions } from "../api/invoices";
import { invoicesMetaQueryOptions } from "../api/meta";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { NewInvoiceModal } from "../pages/invoices";
import { InvoiceTable } from "./invoice-table";
import { ReminderPolicyCard } from "./reminder-policy-card";

export interface CustomerInvoicesPanelProps {
  customerId: number;
  /**
   * Whether to offer "New invoice" — the host reads `invoices:create` and
   * `customers:view`, and offers it only on an active customer the server
   * would let a draft be made for (D8).
   */
  canCreate: boolean;
  /** The signed-in user's name, the "Vår ref." a new draft is prefilled with. */
  userDisplayName?: string;
  /**
   * Whether the caller holds `invoices:payments`, which changing the
   * customer's reminder policy needs (invoices payments and reminders design
   * D7); without it the policy is shown, not changed.
   */
  canChangeReminderPolicy?: boolean;
  /** The signed-in user, so a policy they set says "you". */
  currentUserId?: string;
}

/**
 * The customer page's Invoices tab (D8): the list filtered by the customer,
 * each document with its state and open amount, paged as the list is, and
 * "New invoice" making the draft for this customer and opening it. Below it,
 * the customer's reminder policy.
 */
export const CustomerInvoicesPanel = ({
  customerId,
  canCreate,
  userDisplayName,
  canChangeReminderPolicy = false,
  currentUserId,
}: CustomerInvoicesPanelProps) => {
  const { t, date } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  const [page, setPage] = useState(1);
  const [creating, setCreating] = useState(false);
  const list = useQuery({ ...invoiceListQueryOptions({ customerId, page }), placeholderData: keepPreviousData });

  return (
    <>
      <Card withBorder padding="lg" radius="md" mt="md">
        <Stack gap="md">
          <Group justify="space-between" wrap="wrap">
            <Text fw={600} component="h3">
              {t("invoices")}
            </Text>
            {canCreate && (
              <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setCreating(true)}>
                {t("newInvoice")}
              </Button>
            )}
          </Group>
          {list.isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadInvoices")}>
              {refusalMessage(list.error, t, date)}
            </Alert>
          )}
          {list.isPending && <ContentSkeleton rows={3} rowHeight={48} />}
          {list.data && list.data.data.length === 0 && (
            <EmptyState title={t("noInvoices")} description={t("noInvoicesDescription")} size="sm" />
          )}
          {list.data && list.data.data.length > 0 && (
            <>
              <InvoiceTable rows={list.data.data} showCustomer={false} />
              {list.data.pagination.totalPages > 1 && (
                <Pagination
                  total={list.data.pagination.totalPages}
                  value={list.data.pagination.page}
                  onChange={setPage}
                />
              )}
            </>
          )}
        </Stack>
        {creating && (
          <NewInvoiceModal
            customerId={customerId}
            userDisplayName={userDisplayName}
            deliveryDate={meta.data?.today}
            onClose={() => setCreating(false)}
          />
        )}
      </Card>
      <ReminderPolicyCard customerId={customerId} canChange={canChangeReminderPolicy} currentUserId={currentUserId} />
    </>
  );
};
