import {
  ActionIcon,
  Alert,
  Anchor,
  Button,
  Card,
  Group,
  Modal,
  Stack,
  Table,
  Tabs,
  Text,
  Textarea,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconChevronLeft, IconChevronRight, IconDownload } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { appUrl, ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { downloadVippsPaymentsCsv, reconcileVippsDay, vippsPaymentsDayQueryOptions } from "../api/attempts";
import { isSessionExpired, saveCsv } from "../api/export";
import { invoicesMetaQueryOptions } from "../api/meta";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { invoiceHref, invoiceLinkOptions, PAYMENTS_ROUTE_PATH, VIPPS_PAYMENTS_ROUTE_PATH } from "../lib/routes";

/** A calendar day moved by whole days, in UTC so it never slides. */
const shiftDay = (day: string, by: number): string => {
  const at = new Date(`${day}T00:00:00.000Z`);
  at.setUTCDate(at.getUTCDate() + by);
  return at.toISOString().slice(0, 10);
};

/**
 * The Payments area's two tabs (D22): the bank files and the exception queue,
 * and from 4C the day's Vipps payments. Each is its own route.
 */
export const PaymentsTabs = ({ active }: { active: "bank" | "vipps" }) => {
  const { t } = useInvoiceFormat();
  const navigate = useNavigate() as (options: unknown) => void;
  return (
    <Tabs
      value={active}
      onChange={(value) => navigate({ to: value === "vipps" ? VIPPS_PAYMENTS_ROUTE_PATH : PAYMENTS_ROUTE_PATH })}
    >
      <Tabs.List>
        <Tabs.Tab value="bank">{t("paymentsTab.bank")}</Tabs.Tab>
        <Tabs.Tab value="vipps">{t("paymentsTab.vipps")}</Tabs.Tab>
      </Tabs.List>
    </Tabs>
  );
};

/**
 * One Oslo day's Vipps payments (invoices payments and reminders design D17
 * rule 4, D22), the Payments area's Vipps tab: the day picker — today, meta's,
 * by default — the list on the day Vipps took each payment, with the invoice,
 * the buyer, the way paid, the amount and a refund made since; the count and
 * the totals per way; the day as CSV; and "Mark the day reconciled" for
 * `invoices:payments` (meta's `canRegisterPayments`) once the day is over and
 * not reconciled — the record, once made, said instead.
 */
export const VippsPaymentsPage = () => {
  const { t, date } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader title={t("vippsDay.title")} description={t("vippsDay.description")} />
      <PaymentsTabs active="vipps" />
      {meta.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeta")}>
          {refusalMessage(meta.error, t, date)}
        </Alert>
      )}
      {meta.isPending && <ContentSkeleton rows={4} rowHeight={40} />}
      {meta.data && (
        <VippsDay
          today={meta.data.today}
          currency={meta.data.currency}
          canReconcile={meta.data.capabilities.canRegisterPayments}
        />
      )}
    </Stack>
  );
};

const VippsDay = ({ today, currency, canReconcile }: { today: string; currency: string; canReconcile: boolean }) => {
  const { t, money, date, dateTime } = useInvoiceFormat();
  const navigate = useNavigate() as (options: unknown) => void;
  const [day, setDay] = useState(today);
  const [reconciling, setReconciling] = useState(false);
  const list = useQuery(vippsPaymentsDayQueryOptions(day));
  const csv = useMutation({
    mutationFn: () => downloadVippsPaymentsCsv(day),
    onSuccess: saveCsv,
    onError: (error) => {
      if (isSessionExpired(error)) return;
      notifications.show({
        color: "red",
        title: t("vippsDay.couldNotDownload"),
        message: refusalMessage(error, t, date),
      });
    },
  });
  const amount = (value: number) => money(value, currency);
  const over = day < today;
  const data = list.data;
  return (
    <>
      <Group align="flex-end">
        <ActionIcon
          variant="default"
          size="lg"
          aria-label={t("vippsDay.previous")}
          onClick={() => setDay(shiftDay(day, -1))}
        >
          <IconChevronLeft size={16} />
        </ActionIcon>
        <DateInput
          label={t("vippsDay.day")}
          valueFormat={t("dateInputFormat")}
          value={day}
          maxDate={today}
          onChange={(value) => {
            if (value) setDay(value);
          }}
        />
        <ActionIcon
          variant="default"
          size="lg"
          aria-label={t("vippsDay.next")}
          disabled={day >= today}
          onClick={() => setDay(shiftDay(day, 1))}
        >
          <IconChevronRight size={16} />
        </ActionIcon>
        <Button
          variant="default"
          leftSection={<IconDownload size={16} />}
          loading={csv.isPending}
          onClick={() => csv.mutate()}
        >
          {t("vippsDay.csv")}
        </Button>
        {canReconcile && over && data && !data.reconciliation && (
          <Button onClick={() => setReconciling(true)}>{t("vippsDay.reconcile")}</Button>
        )}
      </Group>
      {list.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("vippsDay.failedToLoad")}>
          {refusalMessage(list.error, t, date)}
        </Alert>
      )}
      {list.isPending && <ContentSkeleton rows={3} rowHeight={40} />}
      {data && (
        <Card withBorder>
          <Stack gap="sm">
            <Group gap="lg" data-testid="vipps-totals">
              <Text size="sm">{t("vippsDay.count", { count: data.count })}</Text>
              <Text size="sm" fw={600}>
                {t("vippsDay.total", { amount: amount(data.total) })}
              </Text>
              <Text size="sm">{t("vippsDay.onSiteTotal", { amount: amount(data.onSiteTotal) })}</Text>
              <Text size="sm">{t("vippsDay.payLinkTotal", { amount: amount(data.payLinkTotal) })}</Text>
            </Group>
            {data.reconciliation ? (
              <Alert color="green" data-testid="vipps-reconciled">
                <Text size="sm">
                  {t("vippsDay.reconciledOn", {
                    at: dateTime(data.reconciliation.reconciledAt),
                    count: data.reconciliation.payments,
                    total: amount(data.reconciliation.total),
                  })}
                </Text>
                {data.reconciliation.note && (
                  <Text size="sm">{t("vippsDay.reconciledNote", { note: data.reconciliation.note })}</Text>
                )}
              </Alert>
            ) : (
              !over &&
              canReconcile && (
                <Text size="sm" c="dimmed">
                  {t("vippsDay.notOver")}
                </Text>
              )
            )}
            {data.payments.length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("vippsDay.none")}
              </Text>
            ) : (
              <Table.ScrollContainer minWidth={640}>
                <Table data-testid="vipps-payments">
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("vippsDay.registeredAt")}</Table.Th>
                      <Table.Th>{t("vippsDay.invoice")}</Table.Th>
                      <Table.Th>{t("vippsDay.buyer")}</Table.Th>
                      <Table.Th>{t("vippsDay.flow")}</Table.Th>
                      <Table.Th ta="right">{t("vippsDay.amount")}</Table.Th>
                      <Table.Th ta="right">{t("vippsDay.refunded")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.payments.map((p) => (
                      <Table.Tr key={p.attemptId}>
                        <Table.Td>{dateTime(p.capturedAt)}</Table.Td>
                        <Table.Td>
                          <Anchor
                            size="sm"
                            href={appUrl(invoiceHref(p.invoiceId))}
                            onClick={(event) => {
                              if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
                              event.preventDefault();
                              navigate(invoiceLinkOptions(p.invoiceId));
                            }}
                          >
                            {t("documentNumbered", { kind: t("kindInvoice"), number: p.invoiceNumber })}
                          </Anchor>
                          <Text size="xs" c="dimmed">
                            {p.reference}
                          </Text>
                        </Table.Td>
                        <Table.Td>{p.buyerName}</Table.Td>
                        <Table.Td>{t(`attempts.flow.${p.flow}`)}</Table.Td>
                        <Table.Td ta="right">{amount(p.amount)}</Table.Td>
                        <Table.Td ta="right">
                          {p.refundedAmount !== undefined && p.refundedAmount > 0 ? amount(p.refundedAmount) : ""}
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}
          </Stack>
        </Card>
      )}
      {reconciling && <ReconcileModal day={day} onClose={() => setReconciling(false)} />}
    </>
  );
};

/**
 * Records the day as reconciled with an optional note (D17 rule 4): once, for
 * good. Every refusal — the day not over, or reconciled already — in words.
 */
const ReconcileModal = ({ day, onClose }: { day: string; onClose: () => void }) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [note, setNote] = useState("");
  const [refused, setRefused] = useState<string | null>(null);
  const reconcile = useMutation({
    mutationFn: () => reconcileVippsDay({ day, note: note.trim() }),
    onMutate: () => setRefused(null),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("vippsDay.reconciledDone") });
      onClose();
    },
    onError: (error) =>
      setRefused(
        error instanceof ApiValidationError
          ? fieldRefusals(error, t, () => false, "vippsDay").elsewhere.join(" ")
          : refusalMessage(error, t, date),
      ),
  });
  return (
    <Modal opened onClose={onClose} title={t("vippsDay.reconcileTitle", { day: date(day) })}>
      <Stack>
        <Text size="sm">{t("vippsDay.reconcileHint")}</Text>
        <Textarea
          label={t("vippsDay.note")}
          maxLength={500}
          value={note}
          onChange={(e) => setNote(e.currentTarget.value)}
        />
        {refused && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("vippsDay.couldNotReconcile")}>
            {refused}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={reconcile.isPending} onClick={() => reconcile.mutate()}>
            {t("vippsDay.reconcileSubmit")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
