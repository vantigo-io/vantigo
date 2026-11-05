import {
  Alert,
  Anchor,
  Box,
  Button,
  Card,
  Group,
  Modal,
  Radio,
  Stack,
  Table,
  Text,
  Title,
  Tooltip,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import {
  IconArrowRight,
  IconCircleCheck,
  IconDeviceFloppy,
  IconLock,
  IconPlayerPause,
  IconPlayerPlay,
  IconPlayerStop,
  IconX,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { NotFoundError, ORDERS_QUERY_KEY } from "../api/request";
import {
  createSubscription,
  type StepInput,
  type SubscriptionStep,
  stepSubscription,
  subscriptionChangesQueryOptions,
  subscriptionQueryOptions,
  updateSubscription,
} from "../api/subscriptions";
import type { FinalPeriod, Subscription } from "../api/types";
import { ChangeLog } from "../components/change-log";
import { ConfirmDialog } from "../components/confirm-dialog";
import { ConflictAlert } from "../components/conflict-alert";
import { CustomerPicker } from "../components/customer-picker";
import { HeaderCard } from "../components/header-card";
import { LineGrid } from "../components/line-grid";
import { PlanFields } from "../components/plan-fields";
import { PeriodStateBadge, SubscriptionStatusBadge } from "../components/status-badges";
import { TotalsCard } from "../components/totals-card";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { newRequestId, useOrdersFormat } from "../lib/format";
import { subscriptionHeaderInput } from "../lib/header";
import { toLineInput } from "../lib/lines";
import { FINAL_PERIODS, type PlanState, planInput } from "../lib/plan";
import { quoteLinkOptions, SUBSCRIPTIONS_ROUTE_PATH, subscriptionLinkOptions } from "../lib/routes";
import { useDocumentWrite, useEditorState } from "../lib/use-editor";

export interface SubscriptionEditorPageProps {
  /** The subscription; absent on the new subscription's page. */
  subscriptionId?: number;
  /** A new subscription's customer, when it was opened from the customer page. */
  customerId?: number;
  access?: OrdersAccess;
}

/**
 * A subscription's editor, new or existing (orders design O42–O44): its plan,
 * header and lines, its periods with each one's state — invoiced, on a draft
 * invoice, skipped, paused, due or upcoming — how far it is billed, and the
 * steps its state and the caller's key allow: activate, pause, resume, end
 * and cancel. A change to an activated subscription's lines takes effect
 * from a period start, the next unbilled one by default; the lines it ended
 * are listed below them.
 */
export const SubscriptionEditorPage = ({
  subscriptionId,
  customerId,
  access = noOrdersAccess,
}: SubscriptionEditorPageProps) => {
  const { t } = useOrdersFormat();
  const subscription = useQuery({
    ...subscriptionQueryOptions(subscriptionId ?? 0),
    enabled: subscriptionId !== undefined,
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
  });
  if (subscriptionId === undefined) return <NewSubscription customerId={customerId} access={access} />;
  if (subscription.isPending) return <ContentSkeleton rows={6} rowHeight={48} />;
  if (subscription.isError)
    return subscription.error instanceof NotFoundError ? (
      <EmptyState title={t("subscriptionNotFound")} description={t("subscriptionNotFoundDescription")} />
    ) : (
      <Alert color="red" title={t("failedToLoadSubscription")}>
        {refusalMessage(subscription.error, t)}
      </Alert>
    );
  return (
    <SubscriptionEditor
      key={`${subscription.data.id}:${subscription.data.version}:${subscription.data.updatedAt}`}
      subscription={subscription.data}
      access={access}
    />
  );
};

const firstOfNextMonth = (): string => {
  const now = new Date();
  const next = new Date(Date.UTC(now.getFullYear(), now.getMonth() + 1, 1));
  return next.toISOString().slice(0, 10);
};

/**
 * A new subscription starts from its customer and its plan; the server fills
 * the header from the customer's billing profile, and the lines are added in
 * the editor it opens.
 */
const NewSubscription = ({ customerId: preset, access }: { customerId?: number; access: OrdersAccess }) => {
  const { t } = useOrdersFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const [customerId, setCustomerId] = useState<number | null>(preset ?? null);
  const [plan, setPlan] = useState<PlanState>({
    interval: "monthly",
    billing: "in_advance",
    startDate: firstOfNextMonth(),
    endDate: null,
    noticeMonths: 0,
    invoiceDay: "",
    finalPeriod: null,
  });
  const [requestId] = useState(newRequestId);
  const create = useMutation({
    mutationFn: () =>
      createSubscription({
        clientRequestId: requestId,
        customerId: customerId as number,
        ...planInput(plan),
        lines: [],
        ...(access.userId ? { salespersonId: access.userId } : {}),
      }),
    onSuccess: async (created) => {
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY] });
      navigate(subscriptionLinkOptions(created.id));
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCreate"), message: refusalMessage(error, t) }),
  });
  const title = t("newSubscriptionTitle");
  return (
    <Stack gap="lg">
      <PageHeader
        title={title}
        breadcrumbs={[{ label: t("subscriptionsTitle"), to: SUBSCRIPTIONS_ROUTE_PATH }, { label: title }]}
      />
      {!access.canManageSubscriptions ? (
        <Alert color="gray" icon={<IconLock size={16} />}>
          {t("mayNotCreateSubscription")}
        </Alert>
      ) : (
        <PageSection maw={980}>
          <Stack>
            <Text size="sm" c="dimmed">
              {t("newSubscriptionHint")}
            </Text>
            {access.canViewCustomers ? (
              <Box data-assist="orders.new-subscription.customer" maw={480}>
                <CustomerPicker value={customerId} onChange={setCustomerId} required />
              </Box>
            ) : preset !== undefined ? (
              <Text size="sm">{t("newForCustomer")}</Text>
            ) : (
              <Alert color="yellow">{t("needsCustomersView")}</Alert>
            )}
            <PlanFields plan={plan} onChange={(patch) => setPlan((current) => ({ ...current, ...patch }))} />
            <Group justify="flex-end">
              <Button
                rightSection={<IconArrowRight size={16} />}
                disabled={customerId === null || !plan.startDate}
                loading={create.isPending}
                onClick={() => create.mutate()}
                data-assist="orders.new-subscription.create"
              >
                {t("createSubscription")}
              </Button>
            </Group>
          </Stack>
        </PageSection>
      )}
    </Stack>
  );
};

type Dialog = "pause" | "resume" | "end" | "cancel" | null;

const planOf = (s: Subscription): PlanState => ({
  interval: s.interval,
  billing: s.billing,
  startDate: s.startDate,
  endDate: s.endDate ?? null,
  noticeMonths: s.noticeMonths,
  invoiceDay: s.invoiceDay ?? "",
  finalPeriod: s.finalPeriod,
});

const SubscriptionEditor = ({ subscription, access }: { subscription: Subscription; access: OrdersAccess }) => {
  const { t, money, date } = useOrdersFormat();
  const queryClient = useQueryClient();
  const editor = useEditorState(subscription);
  const [plan, setPlan] = useState<PlanState>(() => planOf(subscription));
  const [planDirty, setPlanDirty] = useState(false);
  const [effectiveFrom, setEffectiveFrom] = useState<string | null>(subscription.nextChangeFrom);
  const [dialog, setDialog] = useState<Dialog>(null);
  const detailKey = subscriptionQueryOptions(subscription.id).queryKey;
  const changes = useQuery(subscriptionChangesQueryOptions(subscription.id));

  const manage = access.canManageSubscriptions;
  const status = subscription.status;
  const draft = status === "draft";
  const activated = status === "active" || status === "paused" || status === "ended";
  const editable = manage && (draft || activated);
  const dirty = editor.dirty || planDirty;
  const close = () => setDialog(null);
  const common = { detailKey, showRefusal: editor.showRefusal, onDone: close };

  const save = useDocumentWrite({
    ...common,
    write: () => {
      const header = subscriptionHeaderInput(editor.header);
      const { startDate, ...rest } = planInput(plan);
      return updateSubscription(subscription.id, {
        version: subscription.version,
        expectedUpdatedAt: subscription.updatedAt,
        ...header,
        ...(draft
          ? { ...rest, startDate }
          : { noticeMonths: rest.noticeMonths, invoiceDay: rest.invoiceDay, finalPeriod: rest.finalPeriod }),
        ...(activated && effectiveFrom ? { effectiveFrom } : {}),
        lines: editor.lines.map((line) => toLineInput(line, { quote: false })),
      });
    },
    done: t("subscriptionSaved"),
    errorTitle: t("couldNotSave"),
  });
  const step = useDocumentWrite({
    ...common,
    write: ({ name, input }: { name: SubscriptionStep; input?: StepInput }) =>
      stepSubscription(subscription.id, name, subscription, input),
    errorTitle: t("couldNotChangeSubscription"),
  });
  const run = (name: SubscriptionStep, done: string, input?: StepInput) =>
    step.mutate({ name, input }, { onSuccess: () => notifications.show({ color: "green", message: done }) });

  const busy = save.isPending || step.isPending;
  const needsSave = dirty ? t("saveFirst") : undefined;
  const title = t("subscriptionNumber", { number: subscription.number });
  const stepButton = (
    label: string,
    onClick: () => void,
    options: { icon?: ReactNode; color?: string; variant?: string } = {},
  ) => (
    <Tooltip label={needsSave} disabled={!needsSave} withArrow>
      <Button
        leftSection={options.icon}
        color={options.color}
        variant={options.variant ?? "light"}
        disabled={Boolean(needsSave) || busy}
        onClick={onClick}
      >
        {label}
      </Button>
    </Tooltip>
  );
  const sourceQuoteId = subscription.sourceQuoteId;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("subscriptionsTitle"), to: SUBSCRIPTIONS_ROUTE_PATH }, { label: title }]}
        title={
          <Group gap="sm">
            <span>{title}</span>
            <SubscriptionStatusBadge status={status} />
          </Group>
        }
        description={subscription.customer.name}
        actions={
          editable && (
            <Button
              leftSection={<IconDeviceFloppy size={16} />}
              disabled={!dirty}
              loading={save.isPending}
              onClick={() => editor.checkLines() && save.mutate()}
              data-assist="orders.subscription.save"
            >
              {t("save")}
            </Button>
          )
        }
      />
      {editor.conflict && <ConflictAlert onReload={() => void queryClient.refetchQueries({ queryKey: detailKey })} />}
      {sourceQuoteId != null && (
        <Text size="sm" c="dimmed">
          {t("fromQuote")}{" "}
          <Anchor size="sm" renderRoot={(props) => <Link {...quoteLinkOptions(sourceQuoteId)} {...props} />}>
            {t("openQuote")}
          </Anchor>
        </Text>
      )}
      {manage && status !== "cancelled" && (
        <Card withBorder padding="sm" data-testid="subscription-actions">
          <Group gap="xs" wrap="wrap">
            {draft && (
              <span data-assist="orders.subscription.activate">
                {stepButton(t("activate"), () => run("activate", t("subscriptionActivated")), {
                  icon: <IconCircleCheck size={16} />,
                  variant: "filled",
                })}
              </span>
            )}
            {status === "active" && (
              <span data-assist="orders.subscription.pause">
                {stepButton(t("pauseSubscription"), () => setDialog("pause"), {
                  icon: <IconPlayerPause size={16} />,
                  color: "orange",
                })}
              </span>
            )}
            {status === "paused" && (
              <span data-assist="orders.subscription.resume">
                {stepButton(t("resumeSubscription"), () => setDialog("resume"), {
                  icon: <IconPlayerPlay size={16} />,
                })}
              </span>
            )}
            {activated && (
              <span data-assist="orders.subscription.end">
                {stepButton(t("endSubscription"), () => setDialog("end"), {
                  icon: <IconPlayerStop size={16} />,
                })}
              </span>
            )}
            {(draft || status === "active" || status === "paused") &&
              stepButton(t("cancelSubscription"), () => setDialog("cancel"), {
                icon: <IconX size={16} />,
                color: "red",
              })}
          </Group>
        </Card>
      )}

      <PageSection>
        <Stack gap="sm">
          <Group justify="space-between">
            <Title order={4}>{t("plan")}</Title>
            <Group gap="lg">
              <Text size="sm" data-testid="billed-through">
                {t("billedThrough")}:{" "}
                <Text span fw={600}>
                  {subscription.billedThrough ? date(subscription.billedThrough) : "—"}
                </Text>
              </Text>
              {subscription.pausedFrom && (
                <Text size="sm" c="orange">
                  {t("pausedFrom", { date: date(subscription.pausedFrom) })}
                </Text>
              )}
            </Group>
          </Group>
          <PlanFields
            plan={plan}
            disabled={!manage || !draft}
            finalPeriodEditable={editable}
            onChange={(patch) => {
              setPlan((current) => ({ ...current, ...patch }));
              setPlanDirty(true);
            }}
            errors={editor.fieldErrors}
          />
        </Stack>
      </PageSection>

      <HeaderCard
        kind="subscription"
        header={editor.header}
        onChange={editor.patchHeader}
        snapshot={subscription.customer}
        salesperson={subscription.salesperson}
        editable={{ draft: manage && draft, delivery: editable }}
        access={access}
        errors={editor.fieldErrors}
      />

      <PageSection>
        <Stack gap="md">
          <Group justify="space-between" align="flex-end">
            <Title order={4}>{t("lines")}</Title>
            {activated && editable && (
              <DateInput
                label={t("effectiveFrom")}
                description={t("effectiveFromHint", { date: date(subscription.nextChangeFrom) })}
                w={260}
                valueFormat={t("dateInputFormat")}
                minDate={subscription.nextChangeFrom}
                value={effectiveFrom}
                onChange={(value) => setEffectiveFrom(value ?? subscription.nextChangeFrom)}
                error={editor.fieldErrors.effectiveFrom}
                data-assist="orders.subscription.effective-from"
              />
            )}
          </Group>
          <LineGrid
            lines={editor.lines}
            onChange={editor.changeLines}
            mode="order"
            currency={editor.header.currency}
            pricesIncludeVat={editor.header.pricesIncludeVat}
            editable={editable}
            confirmed={false}
            access={access}
            refused={editor.refused}
          />
          <Text size="xs" c="dimmed">
            {t("perPeriodTotals")}
          </Text>
          <TotalsCard totals={subscription.totals} currency={subscription.currency} stale={editor.stale} />
        </Stack>
      </PageSection>

      {subscription.earlierLines.length > 0 && (
        <PageSection data-testid="earlier-lines">
          <Stack gap="sm">
            <Title order={4}>{t("earlierLines")}</Title>
            <Table verticalSpacing="xs" fz="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("line")}</Table.Th>
                  <Table.Th ta="right">{t("quantity")}</Table.Th>
                  <Table.Th ta="right">{t("lineNet")}</Table.Th>
                  <Table.Th>{t("validity")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {subscription.earlierLines.map((line) => (
                  <Table.Tr key={line.id}>
                    <Table.Td>{line.name ?? line.description}</Table.Td>
                    <Table.Td ta="right">{line.quantity ?? ""}</Table.Td>
                    <Table.Td ta="right">{money(line.lineNet, subscription.currency)}</Table.Td>
                    <Table.Td>
                      {line.validFrom ? date(line.validFrom) : t("fromStart")} –{" "}
                      {line.validTo ? date(line.validTo) : ""}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Stack>
        </PageSection>
      )}

      <PageSection data-testid="periods">
        <Stack gap="sm">
          <Title order={4}>{t("periods")}</Title>
          <Table.ScrollContainer minWidth={560}>
            <Table verticalSpacing="xs" fz="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("period")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th ta="right">{t("amountNet")}</Table.Th>
                  <Table.Th>{t("invoices")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {subscription.periods.map((period) => (
                  <Table.Tr key={period.start} data-period={period.start}>
                    <Table.Td>
                      {date(period.start)} – {date(period.end)}
                      {period.partial && (
                        <Text span size="xs" c="dimmed" ml={6}>
                          {t("partialPeriod")}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <PeriodStateBadge state={period.state} />
                    </Table.Td>
                    <Table.Td ta="right" ff="monospace">
                      {money(period.amount, subscription.currency)}
                    </Table.Td>
                    <Table.Td>{period.invoiceNumbers.join(", ")}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Stack>
      </PageSection>

      {!draft && <ChangeLog changes={changes.data?.items ?? []} lines={subscription.lines} />}

      {(dialog === "pause" || dialog === "resume" || dialog === "end") && (
        <DateDialog
          kind={dialog}
          defaultDate={
            dialog === "end"
              ? (subscription.endDate ?? null)
              : dialog === "resume"
                ? (subscription.pausedFrom ?? null)
                : null
          }
          finalPeriod={subscription.finalPeriod}
          loading={step.isPending}
          onSubmit={(value, finalPeriod) => {
            if (dialog === "pause") run("pause", t("subscriptionPaused"), value ? { from: value } : {});
            else if (dialog === "resume") run("resume", t("subscriptionResumed"), { from: value ?? "" });
            else run("end", t("subscriptionEnded"), { endDate: value ?? "", finalPeriod });
          }}
          onClose={close}
        />
      )}
      {dialog === "cancel" && (
        <ConfirmDialog
          title={t("cancelSubscription")}
          confirmLabel={t("cancelSubscription")}
          danger
          loading={step.isPending}
          onConfirm={() => run("cancel", t("subscriptionCancelled"))}
          onClose={close}
        >
          {t("cancelSubscriptionHint")}
        </ConfirmDialog>
      )}
    </Stack>
  );
};

/**
 * A pause's, a resume's or an end's date (O44): a pause from a period start,
 * the next one when left empty; a resume from a period start on or after the
 * pause, the periods between skipped; an end, whose period is the last billed
 * — in full or pro-rated to the end, chosen here and pre-selected from the
 * subscription's own setting.
 */
const DateDialog = ({
  kind,
  defaultDate,
  finalPeriod: initialFinalPeriod,
  loading,
  onSubmit,
  onClose,
}: {
  kind: "pause" | "resume" | "end";
  defaultDate: string | null;
  finalPeriod: FinalPeriod;
  loading: boolean;
  onSubmit: (value: string | null, finalPeriod: FinalPeriod) => void;
  onClose: () => void;
}) => {
  const { t } = useOrdersFormat();
  const [value, setValue] = useState<string | null>(defaultDate);
  const [finalPeriod, setFinalPeriod] = useState<FinalPeriod>(initialFinalPeriod);
  const titles = { pause: t("pauseSubscription"), resume: t("resumeSubscription"), end: t("endSubscription") };
  const hints = { pause: t("pauseHint"), resume: t("resumeHint"), end: t("endHint") };
  const labels = { pause: t("pauseFrom"), resume: t("resumeFrom"), end: t("endDate") };
  return (
    <Modal opened onClose={onClose} title={titles[kind]}>
      <Stack>
        <Text size="sm">{hints[kind]}</Text>
        <DateInput
          label={labels[kind]}
          required={kind !== "pause"}
          clearable={kind === "pause"}
          valueFormat={t("dateInputFormat")}
          value={value}
          onChange={(next) => setValue(next ?? null)}
          data-autofocus
        />
        {kind === "end" && (
          <Radio.Group
            label={t("finalPeriodLabel")}
            value={finalPeriod}
            onChange={(next) => setFinalPeriod(next as FinalPeriod)}
            data-assist="orders.subscription.end-final-period"
          >
            <Stack gap="xs" mt={6}>
              {FINAL_PERIODS.map((option) => (
                <Radio key={option} value={option} label={t(`finalPeriod.${option}`)} />
              ))}
            </Stack>
          </Radio.Group>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancelDialog")}
          </Button>
          <Button loading={loading} disabled={kind !== "pause" && !value} onClick={() => onSubmit(value, finalPeriod)}>
            {titles[kind]}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
