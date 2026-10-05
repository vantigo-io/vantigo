import { Alert, Button, Checkbox, Group, Modal, Radio, Select, SimpleGrid, Stack, Text, Textarea } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { invoiceListQueryOptions, invoiceQueryOptions } from "../api/invoices";
import { invoicesMetaQueryOptions } from "../api/meta";
import { ApiConflictError, ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import { invoiceSettingsQueryOptions } from "../api/settings";
import { vatCodesQueryOptions } from "../api/vat-codes";
import { type FromWorkInput, postFromWork, WORK_KINDS, type WorkHeldBy } from "../api/work";
import { DocumentLink } from "../components/document-link";
import "../i18n";
import { fieldRefusals, refusalProblem, workRefusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { invoiceLinkOptions } from "../lib/routes";
import { type ChosenWork, GROUPINGS, type Grouping, linesFor, totalsOf } from "../lib/work";

/** The VAT code per kind of work the request carries, by the kind's key in `vatCodes`. */
const KIND_KEYS = [
  ["hours", WORK_KINDS.hours],
  ["expenses", WORK_KINDS.expenses],
  ["milestones", WORK_KINDS.milestones],
] as const;
type KindKey = (typeof KIND_KEYS)[number][0];

/** The seeded category-O code (`7`), which a seller not registered for VAT invoices every kind at (D6). */
const NOT_VAT_REGISTERED_CODE = 9;

/** The fields of the request that have an input here; a 400 naming any other is said in the alert. */
const wizardInputs = /^(vatCodes\.(hours|expenses|milestones)|deliveryFrom|deliveryTo|note)$/;

export interface FromWorkWizardProps {
  /** The customer the work is invoiced to — the view's. */
  customerId: number;
  work: ChosenWork[];
  onClose: () => void;
}

/**
 * The wizard (invoices work design D3, D4, D18): the chosen work and its
 * totals, the grouping with the number of lines each would make, the
 * timesheet flag, the VAT code per kind of work the selection has, the
 * delivery period and the note; then "Create draft", or "Add to draft n" for
 * one of the customer's invoice drafts. Every refusal is said in words in the
 * dialog, which stays open: a 409 `source_held_elsewhere` with a link to the
 * document holding the work, `too_many_lines` with the grouping it suggests
 * chosen for the person, a 400 on a VAT code on its own input. On success the
 * draft opens.
 */
export const FromWorkWizard = ({ customerId, work, onClose }: FromWorkWizardProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const meta = useQuery(invoicesMetaQueryOptions());
  const settings = useQuery(invoiceSettingsQueryOptions());
  const allCodes = useQuery(vatCodesQueryOptions());
  const drafts = useQuery(invoiceListQueryOptions({ customerId, status: "draft", kind: "invoice" }));

  const [grouping, setGrouping] = useState<Grouping>("project");
  const [targetId, setTargetId] = useState<number | null>(null);
  const target = useQuery({ ...invoiceQueryOptions(targetId ?? 0), enabled: targetId !== null });
  // Untouched, the request leaves the flag out: the settings' default for a
  // new draft, the target's own flag for an append — which is also what the
  // box shows until the person changes it.
  const [timesheet, setTimesheet] = useState<boolean | null>(null);
  const [vatCodes, setVatCodes] = useState<Partial<Record<KindKey, number>>>({});
  const [deliveryFrom, setDeliveryFrom] = useState<string | null>(null);
  const [deliveryTo, setDeliveryTo] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<{ words: string[]; heldBy?: WorkHeldBy } | null>(null);

  const kinds = KIND_KEYS.filter(([, kind]) => work.some((w) => w.kind === kind));
  const hasHours = work.some((w) => w.kind === WORK_KINDS.hours);
  const timesheetDefault =
    targetId !== null ? (target.data?.timesheet ?? false) : (settings.data?.timesheetDefault ?? false);
  // The defaults the server would take, shown so the person sees what the
  // lines get: the settings' code per kind, or every kind outside the VAT act
  // while the seller is not registered for VAT (D6).
  const defaultCode = (key: KindKey): number | undefined =>
    settings.data
      ? settings.data.vatRegistered
        ? settings.data.workVatCodes[key]
        : NOT_VAT_REGISTERED_CODE
      : undefined;
  const codeOf = (key: KindKey): number | undefined => vatCodes[key] ?? defaultCode(key);
  const vatOptions = (meta.data?.vatCodes ?? []).map((c) => ({
    value: String(c.id),
    label: t("vatCodeOption", { code: c.code, name: c.name }),
  }));
  for (const [key] of kinds) {
    const id = codeOf(key);
    if (id === undefined || vatOptions.some((o) => o.value === String(id))) continue;
    const code = allCodes.data?.find((c) => c.id === id);
    const name = code ? t("vatCodeOption", { code: code.code, name: code.name }) : String(id);
    vatOptions.push({ value: String(id), label: t("vatCodeNotOffered", { label: name }) });
  }

  const input = (): FromWorkInput => ({
    customerId,
    sources: work.map(({ kind, id, revision }) => ({ kind, id, revision })),
    grouping,
    ...(timesheet === null ? {} : { timesheet }),
    vatCodes: Object.fromEntries(
      kinds.flatMap(([key]) => (codeOf(key) === undefined ? [] : [[key, codeOf(key)]])),
    ) as FromWorkInput["vatCodes"],
    ...(deliveryFrom && deliveryTo ? { deliveryFrom, deliveryTo } : {}),
    ...(note.trim() ? { note } : {}),
    ...(targetId !== null && target.data ? { invoiceId: targetId, revision: target.data.revision } : {}),
  });

  const create = useMutation({
    mutationFn: () => postFromWork(input()),
    onMutate: () => setRefusal(null),
    onSuccess: async (draft) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: targetId === null ? t("wizardCreated") : t("wizardAdded") });
      onClose();
      navigate(invoiceLinkOptions(draft.id));
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (field) => wizardInputs.test(field), "fromWork");
        setErrors(onInputs);
        if (elsewhere.length > 0) setRefusal({ words: elsewhere });
        return;
      }
      // A stale revision of the draft the work was going on: read it again.
      if (error instanceof ApiConflictError && !error.code) {
        void target.refetch();
        setRefusal({ words: [t("refusal.invoice_changed")] });
        return;
      }
      const problem = refusalProblem(error);
      const words = [workRefusalMessage(error, t, date)];
      const suggested = typeof problem.suggestedGrouping === "string" ? problem.suggestedGrouping : undefined;
      if (suggested && (GROUPINGS as readonly string[]).includes(suggested)) {
        setGrouping(suggested as Grouping);
        words.push(t("workSuggestedGrouping", { grouping: t(`grouping.${suggested}`) }));
      }
      setRefusal({ words, heldBy: problem.heldBy as WorkHeldBy | undefined });
    },
  });

  const ready = Boolean(settings.data) && (targetId === null || Boolean(target.data));
  const halfPeriod = Boolean(deliveryFrom) !== Boolean(deliveryTo);
  const draftOptions = [
    { value: "new", label: t("wizardNewDraft") },
    ...(drafts.data?.data ?? []).map((d) => ({
      value: String(d.id),
      label: t("wizardExistingDraft", { id: d.id, amount: money(d.grossTotal, d.currency) }),
    })),
  ];

  return (
    <Modal opened onClose={onClose} title={t("wizardTitle")} size="lg">
      <Stack>
        <Text size="sm" data-testid="wizard-selection">
          {t("workSelected", {
            count: work.length,
            amounts: totalsOf(work)
              .map((total) => money(total.amount, total.currency))
              .join(", "),
          })}
        </Text>
        <Radio.Group
          label={t("grouping")}
          description={t("groupingHint")}
          value={grouping}
          onChange={(v) => setGrouping(v as Grouping)}
        >
          <Stack gap={4} mt={4}>
            {GROUPINGS.map((g) => (
              <Radio
                key={g}
                value={g}
                label={t("groupingOption", {
                  grouping: t(`grouping.${g}`),
                  lines: t("groupingLineCount", { count: linesFor(g, work) }),
                })}
              />
            ))}
          </Stack>
        </Radio.Group>
        {hasHours && (
          <Checkbox
            label={t("timesheetFlag")}
            description={t("timesheetFlagHint")}
            checked={timesheet ?? timesheetDefault}
            onChange={(e) => setTimesheet(e.currentTarget.checked)}
          />
        )}
        <SimpleGrid cols={{ base: 1, sm: kinds.length || 1 }}>
          {kinds.map(([key]) => (
            <Select
              key={key}
              label={t(`vatCodeFor.${key}`)}
              data={vatOptions}
              allowDeselect={false}
              value={codeOf(key) === undefined ? null : String(codeOf(key))}
              error={errors[`vatCodes.${key}`]}
              onChange={(v) => {
                if (v === null) return;
                setVatCodes((current) => ({ ...current, [key]: Number(v) }));
                setErrors((current) => {
                  const next = { ...current };
                  delete next[`vatCodes.${key}`];
                  return next;
                });
              }}
            />
          ))}
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <DateInput
            label={t("deliveryFrom")}
            description={t("wizardPeriodHint")}
            valueFormat={t("dateInputFormat")}
            clearable
            value={deliveryFrom}
            error={halfPeriod && !deliveryFrom ? t("periodNeedsBothEnds") : errors.deliveryFrom}
            onChange={setDeliveryFrom}
          />
          <DateInput
            label={t("deliveryTo")}
            valueFormat={t("dateInputFormat")}
            clearable
            value={deliveryTo}
            error={halfPeriod && !deliveryTo ? t("periodNeedsBothEnds") : errors.deliveryTo}
            onChange={setDeliveryTo}
          />
        </SimpleGrid>
        <Textarea
          label={t("note")}
          description={t("wizardNoteHint")}
          maxLength={1000}
          value={note}
          error={errors.note}
          onChange={(e) => setNote(e.currentTarget.value)}
        />
        <Select
          label={t("wizardTarget")}
          data={draftOptions}
          allowDeselect={false}
          value={targetId === null ? "new" : String(targetId)}
          onChange={(v) => setTargetId(!v || v === "new" ? null : Number(v))}
        />
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotInvoiceWork")} role="alert">
            <Stack gap={4}>
              {refusal.words.map((words) => (
                <Text key={words} size="sm">
                  {words}
                </Text>
              ))}
              {refusal.heldBy && (
                <Text size="sm">
                  <DocumentLink invoiceId={refusal.heldBy.invoiceId}>
                    {refusal.heldBy.number
                      ? t("heldOnInvoice", { number: refusal.heldBy.number })
                      : t("heldOnDraft", { id: refusal.heldBy.invoiceId })}
                  </DocumentLink>
                </Text>
              )}
            </Stack>
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={!ready || halfPeriod || work.length === 0}
            loading={create.isPending}
            onClick={() => create.mutate()}
          >
            {targetId === null ? t("wizardCreate") : t("wizardAddTo", { id: targetId })}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
