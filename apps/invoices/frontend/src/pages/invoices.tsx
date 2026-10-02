import { Alert, Button, Group, Modal, Pagination, SegmentedControl, Stack, Text, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useDebouncedValue } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus } from "@tabler/icons-react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { createInvoice, type InvoiceListFilters, invoiceListQueryOptions } from "../api/invoices";
import { invoicesMetaQueryOptions } from "../api/meta";
import { INVOICES_QUERY_KEY } from "../api/request";
import { CustomerPicker } from "../components/customer-picker";
import { InvoiceTable } from "../components/invoice-table";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { invoiceLinkOptions } from "../lib/routes";

export interface InvoicesPageProps {
  /** Whether the caller holds `customers:view`, which the buyer picker needs (D1). The host reads it. */
  canViewCustomers: boolean;
  /** The signed-in user's name, the "Vår ref." a new draft is prefilled with (D4). */
  userDisplayName?: string;
}

/** The states an issued invoice can be filtered by (D3); a draft and a credit note never match one. */
const filterStates = ["open", "partially_paid", "overdue", "paid", "credited"] as const;

/**
 * The list (D4, D12): drafts first, then by number descending, with the
 * status, kind and state chips, a customer filter, a search, an issue-date
 * range and paging; each row badged with its state, and an issued invoice's
 * open amount beside its total. "New invoice" is offered to a caller who may
 * create drafts and pick a buyer.
 */
export const InvoicesPage = ({ canViewCustomers, userDisplayName }: InvoicesPageProps) => {
  const { t, date } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  const [filters, setFilters] = useState<InvoiceListFilters>({ page: 1 });
  // The search is sent once the typing pauses, not per keystroke.
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, 300);
  // The page shown stays while the next filter's is fetched: the table does
  // not flash to a skeleton on every chip, date or page.
  const list = useQuery({
    ...invoiceListQueryOptions({ ...filters, search: debouncedSearch.trim() || undefined }),
    placeholderData: keepPreviousData,
  });
  const [creating, setCreating] = useState(false);
  const set = (next: Partial<InvoiceListFilters>) =>
    setFilters((current) => ({ ...current, ...next, page: next.page ?? 1 }));
  const canCreate = Boolean(meta.data?.capabilities.canCreate) && canViewCustomers;

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("invoices")}
        description={t("invoicesDescription")}
        actions={
          canCreate && (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
              {t("newInvoice")}
            </Button>
          )
        }
      />
      {meta.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeta")}>
          {refusalMessage(meta.error, t, date)}
        </Alert>
      )}
      {meta.data && !meta.data.sellerComplete && meta.data.capabilities.canIssue && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={t("sellerIncompleteTitle")}>
          {t("sellerIncomplete")}
        </Alert>
      )}
      <Group align="flex-end" wrap="wrap">
        <SegmentedControl
          aria-label={t("status")}
          value={filters.status ?? "all"}
          onChange={(v) => set({ status: v === "all" ? undefined : (v as "draft" | "issued") })}
          data={[
            { value: "all", label: t("allStatuses") },
            { value: "draft", label: t("statusDraft") },
            { value: "issued", label: t("statusIssued") },
          ]}
        />
        <SegmentedControl
          aria-label={t("kind")}
          value={filters.kind ?? "all"}
          onChange={(v) => set({ kind: v === "all" ? undefined : (v as "invoice" | "credit_note") })}
          data={[
            { value: "all", label: t("allKinds") },
            { value: "invoice", label: t("kindInvoice") },
            { value: "credit_note", label: t("kindCreditNote") },
          ]}
        />
        <SegmentedControl
          aria-label={t("state")}
          value={filters.state ?? "all"}
          onChange={(v) => set({ state: v === "all" ? undefined : v })}
          data={[
            { value: "all", label: t("allStates") },
            ...filterStates.map((state) => ({ value: state, label: t(`state.${state}`) })),
          ]}
        />
        {canViewCustomers && (
          <CustomerPicker
            label={t("customerFilter")}
            anyStatus
            value={filters.customerId ?? null}
            onChange={(id) => set({ customerId: id ?? undefined })}
          />
        )}
        <TextInput
          label={t("search")}
          placeholder={t("searchPlaceholder")}
          value={search}
          onChange={(e) => {
            setSearch(e.currentTarget.value);
            set({}); // a new search starts on page 1
          }}
        />
        <DateInput
          label={t("issuedFrom")}
          clearable
          valueFormat={t("dateInputFormat")}
          value={filters.from ?? null}
          onChange={(d) => set({ from: d ?? undefined })}
        />
        <DateInput
          label={t("issuedTo")}
          clearable
          valueFormat={t("dateInputFormat")}
          value={filters.to ?? null}
          onChange={(d) => set({ to: d ?? undefined })}
        />
      </Group>
      {list.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadInvoices")}>
          {refusalMessage(list.error, t, date)}
        </Alert>
      )}
      {list.isPending && <ContentSkeleton rows={5} rowHeight={40} />}
      {list.data && list.data.data.length === 0 && (
        <EmptyState title={t("noInvoices")} description={t("noInvoicesDescription")} />
      )}
      {list.data && list.data.data.length > 0 && (
        <>
          <InvoiceTable rows={list.data.data} showCustomer />
          {list.data.pagination.totalPages > 1 && (
            <Pagination
              total={list.data.pagination.totalPages}
              value={list.data.pagination.page}
              onChange={(page) => set({ page })}
            />
          )}
          <Text size="sm" c="dimmed">
            {t("documentCount", { count: list.data.pagination.totalCount })}
          </Text>
        </>
      )}
      {creating && (
        <NewInvoiceModal
          userDisplayName={userDisplayName}
          deliveryDate={meta.data?.today}
          onClose={() => setCreating(false)}
        />
      )}
    </Stack>
  );
};

export interface NewInvoiceModalProps {
  userDisplayName?: string;
  deliveryDate?: string;
  /** The buyer, when the page already knows it — the customer panel's. Without one the person picks it. */
  customerId?: number;
  onClose: () => void;
}

/**
 * Picks the buyer and makes the draft. The API never invents a delivery date;
 * the UI prefills today, which the editor lets the person change (D4).
 */
export const NewInvoiceModal = ({
  userDisplayName,
  deliveryDate,
  customerId: fixed,
  onClose,
}: NewInvoiceModalProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const [customerId, setCustomerId] = useState<number | null>(fixed ?? null);
  const create = useMutation({
    mutationFn: () =>
      createInvoice({
        customerId: customerId as number,
        lines: [],
        ...(deliveryDate ? { deliveryDate } : {}),
        ...(userDisplayName ? { ourReference: userDisplayName } : {}),
      }),
    onSuccess: async (draft) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      onClose();
      navigate(invoiceLinkOptions(draft.id));
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCreate"), message: refusalMessage(error, t, date) }),
  });
  return (
    <Modal opened onClose={onClose} title={t("newInvoice")}>
      <Stack>
        {fixed === undefined ? (
          <CustomerPicker value={customerId} onChange={setCustomerId} required />
        ) : (
          <Text size="sm">{t("newInvoiceForCustomer")}</Text>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button disabled={customerId === null} loading={create.isPending} onClick={() => create.mutate()}>
            {t("createDraft")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
