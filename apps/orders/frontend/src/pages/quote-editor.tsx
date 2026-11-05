import {
  Alert,
  Anchor,
  Button,
  Card,
  Checkbox,
  Group,
  Menu,
  Modal,
  Radio,
  Select,
  Stack,
  Text,
  Title,
  Tooltip,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconAlertTriangle,
  IconCheck,
  IconChevronDown,
  IconCopy,
  IconDeviceFloppy,
  IconDots,
  IconFileText,
  IconMailForward,
  IconSend,
  IconTemplate,
  IconX,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { metaQueryOptions } from "../api/meta";
import {
  acceptQuote,
  applyTemplate,
  cancelQuote,
  copyQuote,
  declineQuote,
  deleteQuote,
  markQuoteSent,
  quoteDocumentsQueryOptions,
  quoteMailingsQueryOptions,
  quotePdfUrl,
  quoteProformaUrl,
  quoteQueryOptions,
  reviseQuote,
  sendQuote,
  updateQuote,
} from "../api/quotes";
import { isVersionConflict, NotFoundError, ORDERS_QUERY_KEY } from "../api/request";
import { templateListQueryOptions } from "../api/templates";
import type { Quote } from "../api/types";
import { overrideOf } from "../api/warnings";
import { ConfirmDialog } from "../components/confirm-dialog";
import { ConflictAlert } from "../components/conflict-alert";
import { DocumentsCard } from "../components/documents-card";
import { HeaderCard } from "../components/header-card";
import { Lifecycle, type LifecycleStep } from "../components/lifecycle";
import { LineGrid } from "../components/line-grid";
import { NewDocument } from "../components/new-document";
import { PdfButton } from "../components/pdf-button";
import { ReasonDialog } from "../components/reason-dialog";
import { SendDialog } from "../components/send-dialog";
import { QuoteStatusBadge } from "../components/status-badges";
import { TotalsCard } from "../components/totals-card";
import { WarningBanners } from "../components/warning-banners";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { addDays, today, useOrdersFormat } from "../lib/format";
import { headerInput } from "../lib/header";
import { toLineInput } from "../lib/lines";
import { QUOTES_ROUTE_PATH, quoteLinkOptions, salesOrderLinkOptions } from "../lib/routes";
import { useDocumentWrite, useEditorState } from "../lib/use-editor";

export interface QuoteEditorPageProps {
  /** The quote; absent on the new quote's page. */
  quoteId?: number;
  /** A new quote's customer, when it was opened from the customer page (O31a). */
  customerId?: number;
  access?: OrdersAccess;
}

/**
 * A quote's editor, new or existing (O13–O25): the header, the line grid,
 * the server's totals, and the actions the quote's state and the caller's
 * keys allow — save, send or mark as sent, revise, accept, decline, cancel,
 * copy, apply a template, the PDFs — with the stored revisions and the sends.
 */
export const QuoteEditorPage = ({ quoteId, customerId, access = noOrdersAccess }: QuoteEditorPageProps) => {
  const { t } = useOrdersFormat();
  const quote = useQuery({
    ...quoteQueryOptions(quoteId ?? 0),
    enabled: quoteId !== undefined,
    // The editor holds the person's edits: the quote is read again only
    // when they ask (a conflict's Reload) or a write answers it.
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
  });
  if (quoteId === undefined) return <NewDocument kind="quote" customerId={customerId} access={access} />;
  if (quote.isPending) return <ContentSkeleton rows={6} rowHeight={48} />;
  if (quote.isError)
    return quote.error instanceof NotFoundError ? (
      <EmptyState title={t("quoteNotFound")} description={t("quoteNotFoundDescription")} />
    ) : (
      <Alert color="red" title={t("failedToLoadQuote")}>
        {refusalMessage(quote.error, t)}
      </Alert>
    );
  return (
    <QuoteEditor
      key={`${quote.data.id}:${quote.data.revision}:${quote.data.updatedAt}`}
      quote={quote.data}
      access={access}
    />
  );
};

const lifecycleOf = (quote: Quote, t: (key: string) => string): LifecycleStep[] => {
  const order = ["draft", "sent", "answered"] as const;
  const at = quote.status === "draft" ? 0 : quote.status === "sent" ? 1 : 2;
  const ending =
    quote.status === "declined" || quote.status === "cancelled"
      ? { label: t(`quoteStatus.${quote.status}`), tone: "red" as const }
      : quote.status === "expired"
        ? { label: t("quoteStatus.expired"), tone: "orange" as const }
        : { label: t("quoteStatus.accepted") };
  return order.map((key, index) => ({
    key,
    label: key === "answered" ? ending.label : t(`quoteStatus.${key}`),
    state: index < at || (index === at && at === 2) ? "done" : index === at ? "current" : "ahead",
    tone: key === "answered" && at === 2 && "tone" in ending ? ending.tone : undefined,
  }));
};

type Dialog = "send" | "markSent" | "accept" | "decline" | "cancel" | "delete" | "template" | null;

const QuoteEditor = ({ quote, access }: { quote: Quote; access: OrdersAccess }) => {
  const { t, date } = useOrdersFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const editor = useEditorState(quote);
  const [dialog, setDialog] = useState<Dialog>(null);
  const [sendError, setSendError] = useState<string>();
  // The reason a settings holder overrides the quote's block warnings with (O33).
  const [override, setOverride] = useState<string | null>(null);
  const detailKey = quoteQueryOptions(quote.id).queryKey;
  const documents = useQuery(quoteDocumentsQueryOptions(quote.id));
  const mailings = useQuery(quoteMailingsQueryOptions(quote.id));
  // Whether Send works now (O25) and the default validity (O20), read under
  // orders:access alone.
  const meta = useQuery(metaQueryOptions());
  const sendingOff = meta.data?.sendingAvailable === false;
  const manage = access.canManageQuotes;
  const draft = quote.status === "draft";
  const editable = draft && manage;
  const close = () => {
    setDialog(null);
    setSendError(undefined);
  };
  // A quote is sent valid until its own date, else the setting's validity from today (O20).
  const validUntilDefault = editor.header.validUntil ?? addDays(today(), meta.data?.quoteValidityDays ?? 30);
  const common = { detailKey, showRefusal: editor.showRefusal, onDone: close };

  const save = useDocumentWrite({
    ...common,
    write: () =>
      updateQuote(quote.id, {
        revision: quote.revision,
        expectedUpdatedAt: quote.updatedAt,
        ...headerInput(editor.header),
        validUntil: editor.header.validUntil,
        lines: editor.lines.map((line) => toLineInput(line, { quote: true })),
      }),
    done: t("quoteSaved"),
    errorTitle: t("couldNotSave"),
  });
  const send = useMutation({
    mutationFn: (input: Omit<Parameters<typeof sendQuote>[1], "expectedUpdatedAt">) =>
      sendQuote(quote.id, {
        ...input,
        revision: quote.revision,
        expectedUpdatedAt: quote.updatedAt,
        ...overrideOf(override),
      }),
    onSuccess: async (sent) => {
      queryClient.setQueryData(detailKey, sent);
      await queryClient.invalidateQueries({
        queryKey: [ORDERS_QUERY_KEY],
        predicate: (q) => q.queryKey[2] !== "detail",
      });
      notifications.show({ color: "green", message: t("quoteSent") });
      // An override is for the one write it was given with (O33).
      setOverride(null);
      close();
    },
    // A refused send is said in the dialog, which stays open to be corrected.
    onError: (error) => {
      if (isVersionConflict(error)) {
        close();
        editor.showRefusal(error, t("couldNotSend"));
      } else setSendError(refusalMessage(error, t));
    },
  });
  const markSent = useDocumentWrite({
    ...common,
    write: () => markQuoteSent(quote.id, quote, validUntilDefault, overrideOf(override)),
    onDone: () => {
      setOverride(null);
      close();
    },
    done: t("quoteMarkedSent"),
    errorTitle: t("couldNotMarkSent"),
  });
  const revise = useDocumentWrite({
    ...common,
    write: () => reviseQuote(quote.id, quote),
    done: t("quoteRevised"),
    errorTitle: t("couldNotRevise"),
  });
  const decline = useDocumentWrite({
    ...common,
    write: (reason: string) => declineQuote(quote.id, quote, reason),
    done: t("quoteDeclined"),
    errorTitle: t("couldNotDecline"),
  });
  const cancel = useDocumentWrite({
    ...common,
    write: () => cancelQuote(quote.id, quote),
    done: t("quoteCancelled"),
    errorTitle: t("couldNotCancel"),
  });
  const copy = useMutation({
    mutationFn: () => copyQuote(quote.id),
    onSuccess: async (copied) => {
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY] });
      notifications.show({ color: "green", message: t("quoteCopied", { number: copied.number }) });
      navigate(quoteLinkOptions(copied.id));
    },
    onError: (error) => editor.showRefusal(error, t("couldNotCopy")),
  });
  const remove = useMutation({
    mutationFn: () => deleteQuote(quote.id, quote.updatedAt),
    onSuccess: async () => {
      queryClient.removeQueries({ queryKey: detailKey });
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY] });
      notifications.show({ color: "green", message: t("quoteDeleted", { number: quote.number }) });
      navigate({ to: QUOTES_ROUTE_PATH });
    },
    onError: (error) => editor.showRefusal(error, t("couldNotDelete")),
  });
  const accept = useMutation({
    mutationFn: (input: { into: "draft_order" | "confirmed_order"; optionalLineIds: number[] }) =>
      acceptQuote(quote.id, {
        revision: quote.revision,
        expectedUpdatedAt: quote.updatedAt,
        ...input,
        ...(input.into === "confirmed_order" ? overrideOf(override) : {}),
      }),
    onSuccess: async (accepted) => {
      queryClient.setQueryData(detailKey, accepted.quote);
      await queryClient.invalidateQueries({
        queryKey: [ORDERS_QUERY_KEY],
        predicate: (q) => q.queryKey[2] !== "detail",
      });
      setOverride(null);
      notifications.show({ color: "green", message: t("quoteAccepted", { number: accepted.order.number }) });
      navigate(salesOrderLinkOptions(accepted.order.id));
    },
    onError: (error) => editor.showRefusal(error, t("couldNotAccept")),
  });
  const apply = useMutation({
    mutationFn: ({ templateId, replace }: { templateId: number; replace: boolean }) =>
      applyTemplate(quote.id, quote, templateId, replace),
    onSuccess: async (applied) => {
      queryClient.setQueryData(detailKey, applied.quote);
      await queryClient.invalidateQueries({
        queryKey: [ORDERS_QUERY_KEY],
        predicate: (q) => q.queryKey[2] !== "detail",
      });
      notifications.show({
        color: applied.skipped.length > 0 ? "yellow" : "green",
        message:
          applied.skipped.length > 0
            ? t("templateAppliedSkipped", {
                lines: applied.skipped.map((line) => line.sku ?? `#${line.variantId}`).join(", "),
              })
            : t("templateApplied"),
      });
      close();
    },
    onError: (error) => editor.showRefusal(error, t("couldNotApplyTemplate")),
  });

  const busy = save.isPending || markSent.isPending || revise.isPending || cancel.isPending || accept.isPending;
  const needsSave = editor.dirty ? t("saveFirst") : undefined;
  const optionalLines = quote.lines.filter((line) => line.optional);
  const title = t("quoteNumber", { number: quote.number });

  const stateButton = (
    label: string,
    onClick: () => void,
    options: { icon?: ReactNode; color?: string; variant?: string; disabledReason?: string } = {},
  ) => (
    <Tooltip label={options.disabledReason ?? needsSave} disabled={!(options.disabledReason ?? needsSave)} withArrow>
      <Button
        leftSection={options.icon}
        color={options.color}
        variant={options.variant ?? "light"}
        disabled={Boolean(options.disabledReason ?? needsSave) || busy}
        onClick={onClick}
      >
        {label}
      </Button>
    </Tooltip>
  );

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("quotesTitle"), to: QUOTES_ROUTE_PATH }, { label: title }]}
        title={
          <Group gap="sm">
            <span>{title}</span>
            {quote.revision > 1 && (
              <Text span size="sm" c="dimmed" fw={500}>
                {t("revisionNumber", { revision: quote.revision })}
              </Text>
            )}
            <QuoteStatusBadge status={quote.status} />
          </Group>
        }
        description={quote.customer.name}
        actions={
          <Group gap="xs">
            {editable && (
              <Button
                leftSection={<IconDeviceFloppy size={16} />}
                disabled={!editor.dirty}
                loading={save.isPending}
                onClick={() => editor.checkLines() && save.mutate()}
                data-assist="orders.quote.save"
              >
                {t("save")}
              </Button>
            )}
            <Menu position="bottom-end" withinPortal>
              <Menu.Target>
                <Button
                  variant="default"
                  rightSection={<IconChevronDown size={14} />}
                  leftSection={<IconFileText size={16} />}
                >
                  {t("pdf")}
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                <PdfMenuItem url={quotePdfUrl(quote.id)} label={draft ? t("previewPdf") : t("downloadPdf")} />
                <PdfMenuItem url={quoteProformaUrl(quote.id)} label={t("proforma")} />
              </Menu.Dropdown>
            </Menu>
            <Menu position="bottom-end" withinPortal>
              <Menu.Target>
                <Button variant="default" aria-label={t("moreActions")} px="xs">
                  <IconDots size={16} />
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                {manage && (
                  <Menu.Item leftSection={<IconCopy size={14} />} onClick={() => copy.mutate()}>
                    {t("copyQuote")}
                  </Menu.Item>
                )}
                {editable && (
                  <Menu.Item
                    leftSection={<IconTemplate size={14} />}
                    onClick={() => setDialog("template")}
                    disabled={editor.dirty}
                  >
                    {t("applyTemplate")}
                  </Menu.Item>
                )}
                {manage && (draft || quote.status === "sent") && (
                  <Menu.Item
                    color="red"
                    leftSection={<IconX size={14} />}
                    onClick={() => setDialog("cancel")}
                    disabled={editor.dirty}
                  >
                    {t("cancelQuote")}
                  </Menu.Item>
                )}
                {manage && draft && !quote.sentAt && (
                  <Menu.Item color="red" onClick={() => setDialog("delete")}>
                    {t("deleteQuote")}
                  </Menu.Item>
                )}
              </Menu.Dropdown>
            </Menu>
          </Group>
        }
      />
      <Lifecycle steps={lifecycleOf(quote, t)} label={t("quoteLifecycle")} />
      <WarningBanners
        warnings={quote.warnings}
        customer={quote.customer}
        access={access}
        blocking={manage && (draft || quote.status === "sent" || quote.status === "expired")}
        override={override}
        onOverride={setOverride}
      />
      {editor.conflict && (
        <ConflictAlert reloading={false} onReload={() => void queryClient.refetchQueries({ queryKey: detailKey })} />
      )}
      {quote.acceptedInto && (
        <Alert color="green" icon={<IconCheck size={16} />}>
          <Group gap={6}>
            <Text size="sm">{t("acceptedInto")}</Text>
            <Anchor
              size="sm"
              fw={600}
              renderRoot={(props) => <Link {...salesOrderLinkOptions(quote.acceptedInto?.id ?? 0)} {...props} />}
            >
              {t("orderNumber", { number: quote.acceptedInto.number })}
            </Anchor>
          </Group>
        </Alert>
      )}
      {quote.status === "declined" && quote.declineReason && (
        <Alert color="red" title={t("declineReason")}>
          {quote.declineReason}
        </Alert>
      )}
      {quote.status === "expired" && (
        <Alert color="orange" icon={<IconAlertTriangle size={16} />}>
          {t("quoteExpiredHint", { date: quote.validUntil ? date(quote.validUntil) : "—" })}
        </Alert>
      )}
      {manage && (
        <Card withBorder padding="sm" data-testid="quote-actions">
          <Group gap="xs" wrap="wrap">
            {draft && (
              <>
                <span data-assist="orders.quote.send">
                  {stateButton(t("send"), () => setDialog("send"), {
                    icon: <IconSend size={16} />,
                    variant: "filled",
                    disabledReason: sendingOff ? t("sendingOff") : undefined,
                  })}
                </span>
                {stateButton(t("markAsSent"), () => setDialog("markSent"), { icon: <IconMailForward size={16} /> })}
              </>
            )}
            {(quote.status === "sent" || quote.status === "expired") && (
              <>
                <span data-assist="orders.quote.accept">
                  {stateButton(t("accept"), () => setDialog("accept"), {
                    icon: <IconCheck size={16} />,
                    variant: "filled",
                    color: "green",
                  })}
                </span>
                {stateButton(t("decline"), () => setDialog("decline"), { color: "red" })}
                {stateButton(t("revise"), () => revise.mutate())}
              </>
            )}
            {!draft && quote.status !== "sent" && quote.status !== "expired" && (
              <Text size="sm" c="dimmed">
                {t("quoteClosed")}
              </Text>
            )}
            {sendingOff && draft && (
              <Text size="xs" c="dimmed">
                {t("sendingOffHint")}
              </Text>
            )}
          </Group>
        </Card>
      )}
      <HeaderCard
        kind="quote"
        header={editor.header}
        onChange={editor.patchHeader}
        snapshot={quote.customer}
        salesperson={quote.salesperson}
        editable={{ draft: editable, delivery: editable }}
        access={access}
        errors={editor.fieldErrors}
      />
      <Card withBorder padding="lg">
        <Stack gap="md">
          <Title order={4}>{t("lines")}</Title>
          <LineGrid
            lines={editor.lines}
            onChange={editor.changeLines}
            mode="quote"
            currency={editor.header.currency}
            pricesIncludeVat={editor.header.pricesIncludeVat}
            editable={editable}
            access={access}
            refused={editor.refused}
          />
          <TotalsCard
            totals={quote.totals}
            currency={quote.currency}
            stale={editor.stale}
            hasOptional={editor.hasOptional}
          />
        </Stack>
      </Card>
      <DocumentsCard
        kind="quote"
        documents={documents.data?.items}
        mailings={mailings.data?.items}
        pdfUrl={(revision) => quotePdfUrl(quote.id, revision)}
      />

      {dialog === "send" && (
        <SendDialog
          kind="quote"
          defaultRecipient={quote.defaultRecipient}
          validUntil={validUntilDefault}
          loading={send.isPending}
          error={sendError}
          onSend={(input) => send.mutate(input)}
          onClose={close}
        />
      )}
      {dialog === "markSent" && (
        <ConfirmDialog
          title={t("markAsSent")}
          confirmLabel={t("markAsSent")}
          loading={markSent.isPending}
          onConfirm={() => markSent.mutate()}
          onClose={close}
        >
          {t("markAsSentHint", { date: date(validUntilDefault) })}
        </ConfirmDialog>
      )}
      {dialog === "decline" && (
        <ReasonDialog
          title={t("declineQuote")}
          description={t("declineHint")}
          label={t("declineReason")}
          submitLabel={t("decline")}
          maxLength={500}
          danger
          loading={decline.isPending}
          onSubmit={(reason) => decline.mutate(reason)}
          onClose={close}
        />
      )}
      {dialog === "cancel" && (
        <ConfirmDialog
          title={t("cancelQuote")}
          confirmLabel={t("cancelQuote")}
          danger
          loading={cancel.isPending}
          onConfirm={() => cancel.mutate()}
          onClose={close}
        >
          {t("cancelQuoteHint")}
        </ConfirmDialog>
      )}
      {dialog === "delete" && (
        <ConfirmDialog
          title={t("deleteQuote")}
          confirmLabel={t("deleteQuote")}
          danger
          loading={remove.isPending}
          onConfirm={() => remove.mutate()}
          onClose={close}
        >
          {t("deleteQuoteHint", { number: quote.number })}
        </ConfirmDialog>
      )}
      {dialog === "accept" && (
        <AcceptDialog
          quote={quote}
          optionalLines={optionalLines}
          canConfirm={access.canConfirmOrders}
          loading={accept.isPending}
          onAccept={(input) => accept.mutate(input)}
          onClose={close}
        />
      )}
      {dialog === "template" && (
        <ApplyTemplateDialog loading={apply.isPending} onApply={(input) => apply.mutate(input)} onClose={close} />
      )}
    </Stack>
  );
};

const PdfMenuItem = ({ url, label }: { url: string; label: string }) => (
  <Menu.Item component="div" p={0} closeMenuOnClick={false}>
    <PdfButton url={url} variant="subtle" color="gray" fullWidth justify="flex-start" size="sm">
      {label}
    </PdfButton>
  </Menu.Item>
);

interface AcceptDialogProps {
  quote: Quote;
  optionalLines: Quote["lines"];
  canConfirm: boolean;
  loading: boolean;
  onAccept: (input: { into: "draft_order" | "confirmed_order"; optionalLineIds: number[] }) => void;
  onClose: () => void;
}

/**
 * Accept (O20, O21): which optional lines the customer took, and whether to
 * make a draft order or — with orders:sales-confirm — a confirmed one. An
 * expired quote asks once more, as honouring a quote a few days late is
 * common but should be meant.
 */
const AcceptDialog = ({ quote, optionalLines, canConfirm, loading, onAccept, onClose }: AcceptDialogProps) => {
  const { t, money } = useOrdersFormat();
  const [chosen, setChosen] = useState<number[]>([]);
  const [into, setInto] = useState<"draft_order" | "confirmed_order">("draft_order");
  const [lateOk, setLateOk] = useState(quote.status !== "expired");
  return (
    <Modal opened onClose={onClose} title={t("acceptQuote")} size="lg">
      <Stack data-assist="orders.quote.accept-into">
        {quote.status === "expired" && (
          <Alert color="orange" icon={<IconAlertTriangle size={16} />}>
            <Checkbox
              label={t("acceptExpired")}
              checked={lateOk}
              onChange={(e) => setLateOk(e.currentTarget.checked)}
            />
          </Alert>
        )}
        {optionalLines.length > 0 && (
          <Checkbox.Group
            label={t("optionalTaken")}
            description={t("optionalTakenHint")}
            value={chosen.map(String)}
            onChange={(values) => setChosen(values.map(Number))}
          >
            <Stack gap={6} mt="xs">
              {optionalLines.map((line) => (
                <Checkbox
                  key={line.id}
                  value={String(line.id)}
                  label={`${line.name ?? line.description} · ${money(line.lineNet, quote.currency)}`}
                />
              ))}
            </Stack>
          </Checkbox.Group>
        )}
        <Radio.Group label={t("acceptInto")} value={into} onChange={(value) => setInto(value as typeof into)}>
          <Stack gap={6} mt="xs">
            <Radio value="draft_order" label={t("acceptIntoDraft")} description={t("acceptIntoDraftHint")} />
            <Radio
              value="confirmed_order"
              label={t("acceptIntoConfirmed")}
              description={canConfirm ? t("acceptIntoConfirmedHint") : t("acceptIntoConfirmedNeeds")}
              disabled={!canConfirm}
            />
          </Stack>
        </Radio.Group>
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancelDialog")}
          </Button>
          <Button
            color="green"
            loading={loading}
            disabled={!lateOk}
            onClick={() => onAccept({ into, optionalLineIds: chosen })}
          >
            {into === "confirmed_order" ? t("acceptAndConfirm") : t("accept")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** Apply a template to a draft quote (O23): its lines appended, or the quote's replaced. */
const ApplyTemplateDialog = ({
  loading,
  onApply,
  onClose,
}: {
  loading: boolean;
  onApply: (input: { templateId: number; replace: boolean }) => void;
  onClose: () => void;
}) => {
  const { t } = useOrdersFormat();
  const templates = useQuery(templateListQueryOptions());
  const [templateId, setTemplateId] = useState<string | null>(null);
  const [replace, setReplace] = useState(false);
  return (
    <Modal opened onClose={onClose} title={t("applyTemplate")}>
      <Stack>
        <Select
          label={t("template")}
          data={(templates.data ?? []).map((tpl) => ({ value: String(tpl.id), label: tpl.name }))}
          value={templateId}
          onChange={setTemplateId}
          nothingFoundMessage={t("noTemplates")}
          data-autofocus
        />
        <Checkbox
          label={t("replaceLines")}
          description={t("replaceLinesHint")}
          checked={replace}
          onChange={(e) => setReplace(e.currentTarget.checked)}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancelDialog")}
          </Button>
          <Button
            loading={loading}
            disabled={templateId === null}
            onClick={() => onApply({ templateId: Number(templateId), replace })}
          >
            {t("applyTemplate")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
