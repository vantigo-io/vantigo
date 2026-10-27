import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Group,
  Modal,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Textarea,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type ConsumptionSettlement,
  consumptionSettlementsQueryOptions,
  type FromConsumptionInput,
  postFromConsumption,
  type SettlementLine,
} from "../api/consumption";
import { invoiceListQueryOptions, invoiceQueryOptions } from "../api/invoices";
import { invoicesMetaQueryOptions } from "../api/meta";
import { ApiConflictError, ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import { invoiceSettingsQueryOptions } from "../api/settings";
import { vatCodesQueryOptions } from "../api/vat-codes";
import type { WorkHeldBy } from "../api/work";
import { DocumentLink } from "../components/document-link";
import { invoicesCatalog } from "../i18n";
import {
  blockerWords,
  consumptionRefusalMessage,
  fieldRefusals,
  refusalCode,
  refusalProblem,
  warningMessage,
} from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { invoiceLinkOptions } from "../lib/routes";

/** The VAT code per hint a settlement line carries, by its key in `vatCodes` (D8). */
const HINT_KEYS = [
  ["energy", "standard"],
  ["energyExempt", "exempt_household_north"],
] as const;
type HintKey = (typeof HINT_KEYS)[number][0];

/** The seeded category-O code (`7`), which a seller not registered for VAT invoices every line at (D8). */
const NOT_VAT_REGISTERED_CODE = 9;

/** The fields of the request that have an input here; a 400 naming any other is said in the alert. */
const wizardInputs = /^(vatCodes\.(energy|energyExempt)|note|through)$/;

export interface FromConsumptionWizardProps {
  /** The customer whose consumption is invoiced — the panel's. */
  customerId: number;
  onClose: () => void;
}

/**
 * The From consumption wizard (energy consumption billing design D7, D8,
 * D11): a through day — today until changed — whose closed billing periods
 * Energy settles and lists, per metering point, each settlement with its
 * period, kWh, amount and lines, and why one cannot be taken: a blocker in
 * words, the document holding it as a link, a yearly supply of a person. The
 * ones that can be taken start chosen. Then the VAT code per hint the chosen
 * lines carry, the note, and "Create draft" or "Add to draft n". The request
 * always names the chosen settlements, so what was seen is what is invoiced.
 * Every refusal is said in words in the dialog, which stays open; on success
 * the draft opens.
 */
export const FromConsumptionWizard = ({ customerId, onClose }: FromConsumptionWizardProps) => {
  const { t, money, unitPrice, number, date, month } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const meta = useQuery(invoicesMetaQueryOptions());
  const settings = useQuery(invoiceSettingsQueryOptions());
  const allCodes = useQuery(vatCodesQueryOptions());
  const drafts = useQuery(invoiceListQueryOptions({ customerId, status: "draft", kind: "invoice", pageSize: 100 }));

  const [through, setThrough] = useState<string | null>(null);
  const day = through ?? meta.data?.today;
  const view = useQuery({
    ...consumptionSettlementsQueryOptions(customerId, day),
    enabled: day !== undefined,
    retry: false,
  });
  // The ones the person unticked: what can be taken starts chosen.
  const [unchecked, setUnchecked] = useState<Set<number>>(new Set());
  const [targetId, setTargetId] = useState<number | null>(null);
  const target = useQuery({ ...invoiceQueryOptions(targetId ?? 0), enabled: targetId !== null });
  const [vatCodes, setVatCodes] = useState<Partial<Record<HintKey, number>>>({});
  const [note, setNote] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<{ words: string[]; heldBy?: WorkHeldBy } | null>(null);

  const settlements = view.data?.settlements ?? [];
  const chosen = settlements.filter((s) => s.selectable && !unchecked.has(s.id));
  const periodOf = (s: ConsumptionSettlement) =>
    t("settlementPeriod", { from: date(s.periodFrom), to: date(s.periodTo) });
  const labelOf = (s: ConsumptionSettlement) => t("settlementLabel", { gsrn: s.gsrn, period: periodOf(s) });
  const points = [...new Set(settlements.map((s) => s.meteringPointId))].map((id) =>
    settlements.filter((s) => s.meteringPointId === id),
  );

  // The hints the chosen lines carry: a code is asked for only where one is.
  const hints = HINT_KEYS.filter(([, hint]) => chosen.some((s) => s.lines.some((l) => l.vatHint === hint)));
  const defaultCode = (key: HintKey): number | undefined =>
    settings.data
      ? settings.data.vatRegistered
        ? settings.data.energyVatCodes?.[key]
        : NOT_VAT_REGISTERED_CODE
      : undefined;
  const codeOf = (key: HintKey): number | undefined => vatCodes[key] ?? defaultCode(key);
  const vatOptions = (meta.data?.vatCodes ?? []).map((c) => ({
    value: String(c.id),
    label: t("vatCodeOption", { code: c.code, name: c.name }),
  }));
  for (const [key] of hints) {
    const id = codeOf(key);
    if (id === undefined || vatOptions.some((o) => o.value === String(id))) continue;
    const code = allCodes.data?.find((c) => c.id === id);
    const name = code ? t("vatCodeOption", { code: code.code, name: code.name }) : String(id);
    vatOptions.push({ value: String(id), label: t("vatCodeNotOffered", { label: name }) });
  }

  const input = (sent: ConsumptionSettlement[]): FromConsumptionInput => {
    const codes = Object.fromEntries(
      hints.flatMap(([key]) => (codeOf(key) === undefined ? [] : [[key, codeOf(key)]])),
    ) as FromConsumptionInput["vatCodes"];
    return {
      customerId,
      ...(day ? { through: day } : {}),
      settlementIds: sent.map((s) => s.id),
      ...(codes && Object.keys(codes).length > 0 ? { vatCodes: codes } : {}),
      ...(note.trim() ? { note } : {}),
      ...(targetId !== null && target.data ? { invoiceId: targetId, revision: target.data.revision } : {}),
    };
  };

  // A refusal names the settlement it is about — by its id on a 409, by its
  // place in the body on a 400 — with its blocker when Energy cannot bill it.
  const refusedRows = (error: unknown, sent: ConsumptionSettlement[]): string[] => {
    const named = (s: ConsumptionSettlement | undefined) => {
      if (!s) return [];
      const blocked = s.blocked ? ` ${blockerWords(s.blocked, t)}${s.blockedDetail ? `: ${s.blockedDetail}` : ""}` : "";
      return [t("consumptionRefusedRow", { label: `${labelOf(s)}${blocked}` })];
    };
    if (error instanceof ApiValidationError) {
      return Object.keys(error.fieldErrors).flatMap((field) => {
        const at = /^settlementIds\[(\d+)\]/.exec(field);
        return at ? named(sent[Number(at[1])]) : [];
      });
    }
    const problem = refusalProblem(error);
    return named(settlements.find((s) => s.id === problem.sourceId));
  };

  const create = useMutation({
    mutationFn: (sent: ConsumptionSettlement[]) => postFromConsumption(input(sent)),
    onMutate: () => setRefusal(null),
    onSuccess: async (draft) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({
        color: "green",
        message: targetId === null ? t("wizardCreated") : t("consumptionAdded"),
      });
      onClose();
      navigate(invoiceLinkOptions(draft.id));
    },
    onError: (error, sent) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (field) => wizardInputs.test(field), "fromConsumption");
        setErrors(onInputs);
        if (elsewhere.length > 0) setRefusal({ words: [...new Set([...elsewhere, ...refusedRows(error, sent)])] });
        return;
      }
      // A stale revision of the draft the consumption was going on: read it again.
      if (error instanceof ApiConflictError && !error.code) {
        void target.refetch();
        setRefusal({ words: [t("refusal.invoice_changed")] });
        return;
      }
      // The consumption moved under the list — held, invoiced, blocked: it is
      // settled and listed again, so what is chosen is what can be chosen now.
      const code = refusalCode(error);
      if (
        code?.startsWith("source_") ||
        code === "settlement_blocked" ||
        code === "no_consumption_due" ||
        code === "invoice_changed"
      ) {
        void queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "consumption"] });
      }
      const problem = refusalProblem(error);
      setRefusal({
        words: [consumptionRefusalMessage(error, t, date), ...refusedRows(error, sent)],
        heldBy: problem.heldBy as WorkHeldBy | undefined,
      });
    },
  });

  const ready = Boolean(settings.data) && (targetId === null || Boolean(target.data));
  const draftOptions = [
    { value: "new", label: t("wizardNewDraft") },
    ...(drafts.data?.data ?? []).map((d) => ({
      value: String(d.id),
      label: t("wizardExistingDraft", { id: d.id, amount: money(d.grossTotal, d.currency) }),
    })),
  ];
  const chosenAmount = Math.round(chosen.reduce((sum, s) => sum + s.amount, 0) * 100) / 100;
  const toggle = (id: number, on: boolean) =>
    setUnchecked((current) => {
      const next = new Set(current);
      if (on) next.delete(id);
      else next.add(id);
      return next;
    });

  const lineText = (line: SettlementLine) => {
    const qualifier = line.band
      ? `band.${line.band}` in invoicesCatalog.en
        ? t(`band.${line.band}`)
        : line.band
      : line.month
        ? month(line.month)
        : undefined;
    return qualifier ? t("settlementLineText", { text: line.text, qualifier }) : line.text;
  };
  const unitWords = (unit: string) =>
    `settlementUnit.${unit}` in invoicesCatalog.en ? t(`settlementUnit.${unit}`) : unit;

  return (
    <Modal opened onClose={onClose} title={t("consumptionWizardTitle")} size="xl">
      <Stack>
        <DateInput
          label={t("consumptionThrough")}
          description={t("consumptionThroughHint")}
          valueFormat={t("dateInputFormat")}
          value={day ?? null}
          error={errors.through}
          onChange={(v) => {
            if (!v) return;
            setThrough(v);
            setUnchecked(new Set());
            setRefusal(null);
          }}
        />
        {view.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadSettlements")}>
            {consumptionRefusalMessage(view.error, t, date)}
          </Alert>
        )}
        {view.isFetching && !view.data && <ContentSkeleton rows={3} rowHeight={40} />}
        {view.data?.warnings.map((w) => (
          <Alert key={w} color="yellow" icon={<IconAlertCircle size={16} />} data-consumption-warning={w}>
            {warningMessage(w, t)}
          </Alert>
        ))}
        {view.data && settlements.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("noSettlements")}
          </Text>
        )}
        {view.data && settlements.length > 0 && (
          <Text size="sm" fw={600} data-testid="consumption-chosen">
            {t("workSelected", { count: chosen.length, amounts: money(chosenAmount, "NOK") })}
          </Text>
        )}
        {points.map((group) => (
          <Stack key={group[0].meteringPointId} gap="xs" data-testid={`metering-point-${group[0].meteringPointId}`}>
            <Text fw={600} size="sm">
              {t("meteringPointHeading", { gsrn: group[0].gsrn, address: group[0].address })}
            </Text>
            {group.map((s) => (
              <Stack
                key={s.id}
                gap={4}
                data-settlement={s.id}
                data-selectable={s.selectable}
                style={s.selectable ? undefined : { opacity: 0.6 }}
              >
                <Group gap="sm" wrap="wrap">
                  <Checkbox
                    aria-label={t("chooseRow", { label: labelOf(s) })}
                    disabled={!s.selectable}
                    checked={s.selectable && !unchecked.has(s.id)}
                    onChange={(e) => toggle(s.id, e.currentTarget.checked)}
                  />
                  <Text size="sm">{periodOf(s)}</Text>
                  {s.kind === "correction" && (
                    <Badge variant="light" color="grape">
                      {t("settlementCorrection")}
                    </Badge>
                  )}
                  <Text size="sm">{t("settlementKwh", { kwh: number(s.kwh, 3) })}</Text>
                  <Text size="sm" fw={600}>
                    {money(s.amount, s.currency)}
                  </Text>
                </Group>
                {s.estimated && (
                  <Text size="xs" c="orange">
                    {t("settlementEstimated")}
                  </Text>
                )}
                {!s.selectable && <SettlementReason settlement={s} />}
                <Table.ScrollContainer minWidth={560}>
                  <Table aria-label={t("settlementLinesOf", { label: labelOf(s) })} fz="xs">
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>{t("settlementLine")}</Table.Th>
                        <Table.Th ta="right">{t("quantity")}</Table.Th>
                        <Table.Th>{t("unit")}</Table.Th>
                        <Table.Th ta="right">{t("unitPrice")}</Table.Th>
                        <Table.Th ta="right">{t("workAmount")}</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {s.lines.map((line, i) => (
                        <Table.Tr key={`${s.id}-${i}`}>
                          <Table.Td>{lineText(line)}</Table.Td>
                          <Table.Td ta="right">{number(line.quantity, 3)}</Table.Td>
                          <Table.Td>{unitWords(line.unit)}</Table.Td>
                          <Table.Td ta="right">{unitPrice(line.unitPrice, s.currency)}</Table.Td>
                          <Table.Td ta="right">{money(line.amount, s.currency)}</Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Table.ScrollContainer>
              </Stack>
            ))}
          </Stack>
        ))}
        {hints.length > 0 && (
          <SimpleGrid cols={{ base: 1, sm: hints.length }}>
            {hints.map(([key]) => (
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
        )}
        <Textarea
          label={t("note")}
          description={t("consumptionNoteHint")}
          maxLength={1000}
          value={note}
          error={errors.note}
          onChange={(e) => setNote(e.currentTarget.value)}
        />
        <Select
          label={t("consumptionTarget")}
          description={drafts.data?.pagination.hasNextPage ? t("wizardDraftsTruncated") : undefined}
          data={draftOptions}
          allowDeselect={false}
          value={targetId === null ? "new" : String(targetId)}
          onChange={(v) => setTargetId(!v || v === "new" ? null : Number(v))}
        />
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotInvoiceConsumption")} role="alert">
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
            disabled={!ready || chosen.length === 0}
            loading={create.isPending}
            onClick={() => create.mutate(chosen)}
          >
            {targetId === null ? t("wizardCreate") : t("wizardAddTo", { id: targetId })}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/**
 * Why a settlement cannot be taken: the document holding it, as a link; the
 * blocker Energy names, with its detail; or a yearly supply of a person.
 */
const SettlementReason = ({ settlement: s }: { settlement: ConsumptionSettlement }) => {
  const { t } = useInvoiceFormat();
  if (s.heldBy) {
    return (
      <Text size="xs">
        <DocumentLink invoiceId={s.heldBy.invoiceId}>
          {s.heldBy.number
            ? t("heldOnInvoice", { number: s.heldBy.number })
            : t("heldOnDraft", { id: s.heldBy.invoiceId })}
        </DocumentLink>
      </Text>
    );
  }
  if (s.blocked) {
    return (
      <Text size="xs" c="orange" data-blocked={s.blocked}>
        {blockerWords(s.blocked, t)}
        {s.blockedDetail ? ` — ${s.blockedDetail}` : ""}
      </Text>
    );
  }
  if (s.reason === "cadence") {
    return (
      <Text size="xs" c="dimmed">
        {t("settlementReason.cadence")}
      </Text>
    );
  }
  if (!s.reason) return null;
  return (
    <Text size="xs" c="dimmed">
      {`settlementReason.${s.reason}` in invoicesCatalog.en ? t(`settlementReason.${s.reason}`) : s.reason}
    </Text>
  );
};
