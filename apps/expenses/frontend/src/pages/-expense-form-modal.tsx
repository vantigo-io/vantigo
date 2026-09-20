import {
  Alert,
  Button,
  Divider,
  Group,
  Input,
  Modal,
  NumberInput,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { deleteReceipt } from "../api/attachments";
import type { Claim } from "../api/claims";
import {
  ApiValidationError,
  createExpense,
  type Expense,
  type ExpenseAttachment,
  type ExpenseInput,
  type ExpenseUpdateInput,
  submitExpenses,
  updateExpense,
} from "../api/entries";
import { expensesMetaQueryOptions } from "../api/meta";
import type { ExpenseProjectOption } from "../api/projects";
import { expenseRatesQueryOptions } from "../api/rates";
import { type ApiError, EXPENSES_QUERY_KEY } from "../api/request";
import { EntryDetails } from "../components/entry-details";
import { ReceiptDropzone } from "../components/receipt-dropzone";
import { ReceiptThumbnails } from "../components/receipt-thumbnails";
import { RefusalList } from "../components/refusal-list";
import { VatField } from "../components/vat-field";
import "../i18n";
import { today } from "../lib/dates";
import { refusalMessage, refusalMessages } from "../lib/errors";
import { useDecimalSeparator, useExpenseFormat } from "../lib/format";
import {
  DESCRIPTION_MAX_LENGTH,
  MAX_DISTANCE_KM,
  MAX_GROSS,
  MAX_PASSENGERS,
  PLACE_MAX_LENGTH,
  round1,
  type VatChoice,
  vatFromGross,
} from "../lib/money";
import { useProjectOptions } from "../lib/project-options";
import { mileagePreview } from "../lib/rates";
import type { ExpenseKind, PaidBy } from "../lib/status";
import { zoneCalendarDate } from "../lib/time-zone";

/**
 * Recording a new expense, or changing one the caller read off their list.
 *
 * A `claim` makes it a **line of a travel claim**: the kind is fixed by the
 * section it was opened from, the project block is replaced by the trip's own
 * project, and there is no "save and submit" — a trip is submitted whole.
 *
 * A `project` makes it a **cost recorded from that project's own page**: the
 * project is stated rather than offered, because the form was opened from it
 * and pointing it somewhere else would be a different action; the billing
 * line is picked from that project's lines. It is still an expense of its
 * own, so it can be saved and submitted in one go.
 */
export type ExpenseModalState =
  | { mode: "create"; kind?: ExpenseKind; claim?: Claim; project?: ExpenseProjectOption }
  | { mode: "edit"; expense: Expense; claim?: Claim };

export interface ExpenseFormModalProps {
  state: ExpenseModalState | null;
  onClose: () => void;
  /**
   * The expense a save answered with. A page that shows figures the save
   * moved — the project page's Expenses tab — needs to know it happened; the
   * modal's own cache invalidation cannot reach another module's.
   */
  onSaved?: (expense: Expense) => void;
}

interface ExpenseFormValues {
  kind: ExpenseKind;
  entryDate: string | null;
  description: string;
  categoryId: string | null;
  supplier: string;
  grossAmount: number | string;
  vatAmount: number | string;
  paidBy: PaidBy;
  fromPlace: string;
  toPlace: string;
  distanceKm: number | string;
  passengers: number | string;
  projectId: string | null;
  billingLineId: string | null;
  billable: boolean;
}

/** The request fields a refusal may name that this form has an input for. */
const formFields = new Set([
  "kind",
  "entryDate",
  "description",
  "categoryId",
  "supplier",
  "grossAmount",
  "vatAmount",
  "paidBy",
  "fromPlace",
  "toPlace",
  "distanceKm",
  "passengers",
  "projectId",
  "billingLineId",
  "billable",
]);

const numeric = (value: number | string): number | undefined => {
  if (typeof value === "number") return Number.isFinite(value) ? value : undefined;
  const trimmed = value.trim().replace(",", ".");
  if (trimmed === "") return undefined;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : undefined;
};

/**
 * One expense, whichever kind it is. The kind switches the *payload* and not
 * merely which inputs are on screen: a mileage line carries no category, no
 * supplier, no payer, no gross and no VAT, and naming one of them is a 400 on
 * that field.
 *
 * The form lives in `ExpenseForm`, which the modal mounts fresh every time it
 * opens, so no previous expense's values survive a close.
 */
export const ExpenseFormModal = ({ state, onClose, onSaved }: ExpenseFormModalProps) => {
  const { t } = useI18n("expenses");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editExpenseTitle") : t("newExpenseTitle")}
      centered
      size="lg"
    >
      {state && (
        <ExpenseForm
          key={state.mode === "edit" ? state.expense.id : "create"}
          state={state}
          onClose={onClose}
          onSaved={onSaved}
        />
      )}
    </Modal>
  );
};

const ExpenseForm = ({
  state,
  onClose,
  onSaved,
}: {
  state: ExpenseModalState;
  onClose: () => void;
  onSaved?: (expense: Expense) => void;
}) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const decimalSeparator = useDecimalSeparator();
  const queryClient = useQueryClient();

  const opened = state.mode === "edit" ? state.expense : undefined;
  /**
   * The travel claim this line belongs to, when it is one. Everything the
   * claim decides — the owner, the project, when it may be changed and when
   * it is submitted — is the claim's, so the form neither asks nor sends it.
   */
  const claim = state.claim;
  /**
   * The project this cost is being recorded on, when the form was opened from
   * that project's own page. It is stated, never offered: the page the form
   * came from *is* the answer, and a picker here would let somebody book a
   * cost on a project they never meant to open.
   */
  const fixedProject = state.mode === "create" ? state.project : undefined;
  /**
   * What the form is editing. A create becomes an edit as soon as it is
   * saved, so its receipts can be added without closing and reopening.
   */
  const [saved, setSaved] = useState<Expense | undefined>(opened);
  /**
   * The revision the form was opened at — never a refetched one. A save
   * answers with the expense as it now stands, and that answer is the only
   * thing that moves it on.
   */
  const [revision, setRevision] = useState<number | undefined>(opened?.revision);
  const [attachments, setAttachments] = useState<ExpenseAttachment[]>(opened?.attachments ?? []);
  const [refusals, setRefusals] = useState<string[]>([]);
  /** Which rate the VAT helper is set to; the gross input needs it too. */
  const [vatChoice, setVatChoice] = useState<VatChoice>("none");

  const { data: meta } = useQuery(expensesMetaQueryOptions());
  const { projects, options: projectOptions } = useProjectOptions(opened?.project, meta?.projectsAvailable === true);
  const { data: rates } = useQuery(expenseRatesQueryOptions());

  const readOnly = saved !== undefined && !saved.capabilities.canEdit;
  const currency = saved?.currency ?? meta?.defaultCurrency ?? "";
  /**
   * The lock, as the **unit** is judged by it. A standalone expense is judged
   * on its own date; a line of a travel claim is judged on the claim's
   * departure day, so a trip that left after the lock may hold a ticket
   * bought before it and the form must neither refuse the day nor claim it is
   * closed. `expenses:manage` is never held back either.
   */
  const lockedBefore = claim || meta?.capabilities.canManage ? undefined : meta?.lockedBefore;

  /**
   * What a new line starts on. Inside a trip that is the **departure day in
   * the installation's zone** — a March trip recorded in September has no use
   * for today, and the day has to be the one the server judges the trip by.
   */
  const startsOn = claim ? zoneCalendarDate(claim.departureAt, meta?.timeZone ?? "UTC") : today();

  const form = useForm<ExpenseFormValues>({
    initialValues: {
      kind: opened?.kind ?? (state.mode === "create" ? (state.kind ?? "outlay") : "outlay"),
      entryDate: opened?.entryDate ?? startsOn,
      description: opened?.description ?? "",
      categoryId: opened?.category ? String(opened.category.id) : null,
      supplier: opened?.supplier ?? "",
      grossAmount: opened && opened.kind === "outlay" ? opened.grossAmount : "",
      vatAmount: opened?.vatAmount ?? "",
      // A cost booked from a project's own page is the company's spending on
      // it far more often than somebody's own pocket, so that is what it
      // starts on; the control is still there and still switches the payload.
      paidBy: opened?.paidBy ?? (fixedProject ? "company" : "employee"),
      fromPlace: opened?.fromPlace ?? "",
      toPlace: opened?.toPlace ?? "",
      distanceKm: opened?.distanceKm ?? "",
      passengers: opened?.passengers ?? 0,
      projectId: opened?.project ? String(opened.project.id) : fixedProject ? String(fixedProject.id) : null,
      billingLineId: opened?.billingLine ? String(opened.billingLine.id) : null,
      billable: opened?.billable ?? false,
    },
    validate: {
      entryDate: (value) => (value ? null : t("dateRequired")),
      description: (value) => {
        const description = value.trim();
        if (!description) return t("descriptionRequired");
        return description.length > DESCRIPTION_MAX_LENGTH ? t("descriptionTooLong") : null;
      },
      categoryId: (value, values) => (values.kind === "outlay" && !value ? t("categoryRequired") : null),
      supplier: (value) => (value.trim().length > PLACE_MAX_LENGTH ? t("supplierTooLong") : null),
      fromPlace: (value) => (value.trim().length > PLACE_MAX_LENGTH ? t("placeTooLong") : null),
      toPlace: (value) => (value.trim().length > PLACE_MAX_LENGTH ? t("placeTooLong") : null),
      grossAmount: (value, values) => {
        if (values.kind !== "outlay") return null;
        const gross = numeric(value);
        if (gross === undefined || gross <= 0) return t("grossRequired");
        return gross > MAX_GROSS ? t("grossTooLarge") : null;
      },
      vatAmount: (value, values) => {
        if (values.kind !== "outlay") return null;
        const vat = numeric(value);
        const gross = numeric(values.grossAmount) ?? 0;
        return vat !== undefined && vat > gross ? t("vatNotAboveGross") : null;
      },
      distanceKm: (value, values) => {
        if (values.kind !== "mileage") return null;
        const distance = numeric(value);
        if (distance === undefined || distance <= 0) return t("distanceRequired");
        return distance > MAX_DISTANCE_KM ? t("distanceTooLarge") : null;
      },
      passengers: (value, values) => {
        if (values.kind !== "mileage") return null;
        const passengers = numeric(value) ?? 0;
        return passengers < 0 || passengers > MAX_PASSENGERS ? t("passengersRange") : null;
      },
    },
  });

  const values = form.values;
  const gross = numeric(values.grossAmount) ?? 0;
  const distance = numeric(values.distanceKm) ?? 0;
  const passengers = numeric(values.passengers) ?? 0;
  const preview =
    values.kind === "mileage" && values.entryDate && distance > 0
      ? mileagePreview(rates, values.entryDate, distance, passengers)
      : undefined;
  const missingRate = values.kind === "mileage" && distance > 0 && rates !== undefined && preview === undefined;

  // The fixed project carries its own billing lines, so the picker is filled
  // even before `GET /projects` answers — and on a project whose options the
  // caller would not otherwise be offered.
  const chosenProject = fixedProject ?? projects?.find((project) => String(project.id) === values.projectId);
  const keptLine =
    opened?.billingLine &&
    projects !== undefined &&
    String(opened.project?.id ?? "") === values.projectId &&
    !chosenProject?.billingLines.some((line) => line.id === opened.billingLine?.id)
      ? opened.billingLine
      : undefined;
  const lineOptions = [
    ...(chosenProject?.billingLines ?? []).map((line) => ({ value: String(line.id), label: line.code })),
    ...(keptLine ? [{ value: String(keptLine.id), label: keptLine.code }] : []),
  ];

  const categoryOptions = (meta?.categories ?? [])
    .filter((category) => category.active || String(category.id) === values.categoryId)
    .map((category) => ({ value: String(category.id), label: category.name }));

  const needsReceipt =
    values.kind === "outlay" &&
    values.paidBy === "employee" &&
    meta?.receiptRequiredOver !== undefined &&
    gross > meta.receiptRequiredOver &&
    attachments.length === 0;

  /**
   * The payload for the kind on screen. Project, line and billable go along
   * only where there are projects at all and one is chosen; the markup and
   * the customer rate never do — they are the project's own figures, and a
   * form that names one is refused on that field.
   */
  const payload = (): ExpenseInput => {
    const shared = {
      kind: values.kind,
      entryDate: values.entryDate ?? "",
      description: values.description.trim(),
      // A line's project is always its claim's — naming another one is a 400
      // — so inside a trip only the line's own billable flag is sent, and
      // only where the trip is booked on something at all.
      ...(claim
        ? { claimId: claim.id, ...(meta?.projectsAvailable && claim.project ? { billable: values.billable } : {}) }
        : meta?.projectsAvailable && values.projectId
          ? {
              projectId: Number(values.projectId),
              ...(values.billingLineId ? { billingLineId: Number(values.billingLineId) } : {}),
              billable: values.billable,
            }
          : {}),
    };
    if (values.kind === "mileage") {
      return {
        ...shared,
        distanceKm: round1(distance),
        ...(passengers > 0 ? { passengers } : {}),
        ...(values.fromPlace.trim() ? { fromPlace: values.fromPlace.trim() } : {}),
        ...(values.toPlace.trim() ? { toPlace: values.toPlace.trim() } : {}),
      };
    }
    const vat = numeric(values.vatAmount);
    return {
      ...shared,
      currency,
      categoryId: Number(values.categoryId),
      paidBy: values.paidBy,
      grossAmount: gross,
      ...(vat !== undefined ? { vatAmount: vat } : {}),
      ...(values.supplier.trim() ? { supplier: values.supplier.trim() } : {}),
    };
  };

  const onRefusal = (error: Error) => {
    if (error instanceof ApiValidationError) {
      const fields = Object.fromEntries(Object.entries(error.fieldErrors).filter(([field]) => formFields.has(field)));
      if (Object.keys(fields).length > 0) {
        form.setErrors(fields);
        return;
      }
      // A refusal on `claimId` is a fact about the trip, not about this line:
      // it changed underneath, so the page behind the modal is reading a copy
      // that no longer holds — including which doors it still offers.
      if (error.fieldErrors.claimId !== undefined) {
        void queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      }
      setRefusals(refusalMessages(error));
      return;
    }
    const conflict = (error as ApiError).status === 409;
    notifications.show({
      color: "red",
      title: t("couldNotSaveExpense"),
      message: conflict ? t("expenseChangedElsewhere") : refusalMessage(error),
    });
  };

  /** Saves, and — when asked — sends the saved expense for approval at once. */
  const save = useMutation({
    mutationFn: async ({ andSubmit }: { andSubmit: boolean }) => {
      const input = payload();
      const stored =
        saved && revision !== undefined
          ? await updateExpense(saved.id, { ...input, revision } as ExpenseUpdateInput)
          : await createExpense(input);
      if (!andSubmit) return { stored, submitted: false as const };
      const moved = await submitExpenses([stored.id]).catch(async (error: Error) => {
        // The expense is saved either way; only the submission was refused.
        // The list has to learn about it here, because `onSuccess` — where
        // every other write invalidates — is not going to run: a new draft
        // that is nowhere on screen is one somebody records a second time.
        // And so does whoever is showing figures this package cannot reach:
        // the draft is in the project's draft bucket and in its cost either
        // way, so the save moved them even though the submission did not.
        setSaved(stored);
        setRevision(stored.revision);
        setAttachments(stored.attachments);
        await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
        onSaved?.(stored);
        throw error;
      });
      return { stored: moved.entries[0] ?? stored, submitted: true as const };
    },
    onSuccess: async ({ stored, submitted }) => {
      setRefusals([]);
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      // A new outlay stays open once it is a draft: its receipts can only be
      // attached to something that exists, and asking somebody to reopen the
      // form they just filled in to add them would be a poor trade.
      const keepOpen = !submitted && saved === undefined && stored.kind === "outlay";
      onSaved?.(stored);
      setSaved(stored);
      setRevision(stored.revision);
      setAttachments(stored.attachments);
      notifications.show({
        color: "teal",
        title: submitted ? t("expensesSubmitted") : keepOpen ? t("draftSaved") : t("expenseSaved"),
        message: keepOpen ? t("draftSavedMessage") : stored.description,
      });
      if (!keepOpen) onClose();
    },
    onError: onRefusal,
  });

  const removeReceipt = useMutation({
    mutationFn: (id: number) => deleteReceipt(id),
    onSuccess: async (_result, id) => {
      setAttachments((current) => current.filter((one) => one.id !== id));
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("receiptRemoved"), message: "" });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotRemoveReceipt"), message: refusalMessage(error) }),
  });

  const submit = (andSubmit: boolean) =>
    form.onSubmit(() => {
      setRefusals([]);
      save.mutate({ andSubmit });
    });

  const billing = saved?.capabilities.canSeeBilling ? saved.billing : undefined;

  // An expense the caller may see but not change is written out in full
  // rather than shown as a form nothing in it can be typed into — the same
  // rendering the approval drawer uses.
  if (readOnly && saved) {
    return (
      <Stack>
        <Alert color="gray">{t("readOnlyNotice")}</Alert>
        <EntryDetails expense={saved} />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onClose}>
            {t("close")}
          </Button>
        </Group>
      </Stack>
    );
  }

  return (
    <form onSubmit={submit(false)}>
      <Stack>
        {saved?.decision?.status === "rejected" && saved.decision.reason && (
          <Alert color="red" title={t("rejectedBecause", { reason: saved.decision.reason })}>
            {/* `by` is optional in the contract, for a stored decision with no
                decider that no operation can produce; the reason stands on its
                own when there is nobody to name. */}
            {saved.decision.by
              ? t("rejectedBy", { person: saved.decision.by.displayName, date: format.dateTime(saved.decision.at) })
              : t("rejectedOn", { date: format.dateTime(saved.decision.at) })}
          </Alert>
        )}
        <RefusalList messages={refusals} />

        {claim === undefined && (
          <Input.Wrapper label={t("kind")} labelElement="div">
            <SegmentedControl
              fullWidth
              mt={4}
              disabled={saved !== undefined}
              aria-label={t("kind")}
              value={values.kind}
              onChange={(next) => form.setFieldValue("kind", next as ExpenseKind)}
              data={[
                { value: "outlay", label: t("kindOutlay") },
                { value: "mileage", label: t("kindMileage") },
              ]}
            />
          </Input.Wrapper>
        )}

        <Group grow align="start">
          <DateInput
            label={t("date")}
            valueFormat={t("dateInputFormat")}
            withAsterisk
            minDate={lockedBefore}
            description={lockedBefore ? t("lockedBeforeHint", { date: format.date(lockedBefore) }) : undefined}
            {...form.getInputProps("entryDate")}
          />
          <TextInput label={t("description")} withAsterisk data-autofocus {...form.getInputProps("description")} />
        </Group>

        {values.kind === "outlay" ? (
          <Stack>
            <Group grow align="start">
              <Select
                label={t("category")}
                placeholder={t("chooseCategory")}
                withAsterisk
                searchable
                data={categoryOptions}
                {...form.getInputProps("categoryId")}
              />
              <TextInput label={t("supplier")} {...form.getInputProps("supplier")} />
            </Group>
            <Group grow align="start">
              <NumberInput
                label={t("grossAmount")}
                description={currency}
                withAsterisk
                min={0}
                decimalScale={2}
                decimalSeparator={decimalSeparator}
                {...form.getInputProps("grossAmount")}
                onChange={(next) => {
                  form.setFieldValue("grossAmount", next);
                  // A gross corrected under a chosen rate takes the VAT with
                  // it: "25 %" must never stand over a figure that is not it.
                  if (vatChoice !== "none") {
                    const corrected = numeric(next) ?? 0;
                    form.setFieldValue("vatAmount", corrected > 0 ? vatFromGross(corrected, vatChoice) : "");
                  }
                }}
              />
              <VatField
                gross={gross}
                currency={currency}
                value={values.vatAmount}
                error={form.errors.vatAmount}
                choice={vatChoice}
                onChoiceChange={setVatChoice}
                onChange={(next) => form.setFieldValue("vatAmount", next)}
              />
            </Group>
            <Input.Wrapper label={t("paidBy")} labelElement="div">
              <SegmentedControl
                mt={4}
                aria-label={t("paidBy")}
                value={values.paidBy}
                onChange={(next) => form.setFieldValue("paidBy", next as PaidBy)}
                data={[
                  { value: "employee", label: t("paidByEmployee") },
                  { value: "company", label: t("paidByCompany") },
                ]}
              />
            </Input.Wrapper>
          </Stack>
        ) : (
          <Stack>
            <Group grow align="start">
              <TextInput label={t("fromPlace")} {...form.getInputProps("fromPlace")} />
              <TextInput label={t("toPlace")} {...form.getInputProps("toPlace")} />
            </Group>
            <Group grow align="start">
              <NumberInput
                label={t("distanceKm")}
                withAsterisk
                min={0}
                decimalScale={1}
                decimalSeparator={decimalSeparator}
                {...form.getInputProps("distanceKm")}
              />
              <NumberInput
                label={t("passengers")}
                min={0}
                max={MAX_PASSENGERS}
                allowDecimal={false}
                {...form.getInputProps("passengers")}
              />
            </Group>
            {preview && (
              <Stack gap={0}>
                <Text size="sm" data-testid="mileage-preview">
                  {t("mileagePreview", {
                    km: format.distance(distance),
                    rate: format.number(preview.rate, 2),
                    amount: format.money(preview.amount, currency),
                  })}
                </Text>
                <Text size="xs" c="dimmed">
                  {t("mileagePreviewNote")}
                </Text>
              </Stack>
            )}
            {missingRate && (
              <Text size="sm" c="orange">
                {t("noRateForDate")}
              </Text>
            )}
          </Stack>
        )}

        {claim !== undefined && meta?.projectsAvailable && claim.project && (
          <>
            <Divider />
            <Stack gap="xs">
              <Text size="sm">
                {t("bookedOnProject", { project: `${claim.project.code} · ${claim.project.name}` })}
              </Text>
              <Text size="xs" c="dimmed">
                {t("lineFollowsClaim")}
              </Text>
              <Switch label={t("billable")} {...form.getInputProps("billable", { type: "checkbox" })} />
              {billing && (
                <Stack gap={2} data-testid="expense-billing">
                  <Title order={6}>{t("billingHeading")}</Title>
                  <Text size="sm">{`${t("billAmount")}: ${format.money(billing.billAmount, currency)}`}</Text>
                </Stack>
              )}
            </Stack>
          </>
        )}

        {/* Recorded from a project's own page: the project is a sentence, not
            a picker, exactly as a trip's line states the trip's project. The
            billing line and the billable flag are still the caller's to set —
            they are about this cost, not about which project it is on. */}
        {claim === undefined && fixedProject !== undefined && meta?.projectsAvailable && (
          <>
            <Divider />
            <Stack gap="xs">
              {/* The project is a sentence rather than an input, but it is
                  still a field the server can refuse: a create is never
                  grandfathered, so a project completed — or a person taken off
                  its team — while the form was open comes back as a 400 on
                  `projectId`. Without a wrapper to carry it, "Save draft"
                  would sit there having done nothing and said nothing. */}
              <Input.Wrapper error={form.errors.projectId}>
                <Text size="sm">
                  {t("bookedOnProject", { project: `${fixedProject.code} · ${fixedProject.name}` })}
                </Text>
                <Text size="xs" c="dimmed">
                  {t("costFollowsProject")}
                </Text>
              </Input.Wrapper>
              <Select
                label={t("billingLine")}
                placeholder={t("noBillingLine")}
                clearable
                disabled={lineOptions.length === 0}
                data={lineOptions}
                {...form.getInputProps("billingLineId")}
              />
              <Switch label={t("billable")} {...form.getInputProps("billable", { type: "checkbox" })} />
              {values.billable && billing === undefined && (
                <Text size="xs" c="dimmed">
                  {t("pricingIsTheProjects")}
                </Text>
              )}
            </Stack>
          </>
        )}

        {claim === undefined && fixedProject === undefined && meta?.projectsAvailable && (
          <>
            <Divider />
            {projectOptions.length === 0 && !values.projectId ? (
              <Text size="sm" c="dimmed">
                {t("noBookableProjects")}
              </Text>
            ) : (
              <Stack gap="xs">
                <Group grow align="start">
                  <Select
                    label={t("project")}
                    placeholder={t("chooseProject")}
                    clearable
                    searchable
                    data={projectOptions}
                    value={values.projectId}
                    error={form.errors.projectId}
                    onChange={(next) => form.setValues({ projectId: next, billingLineId: null })}
                  />
                  <Select
                    label={t("billingLine")}
                    placeholder={t("noBillingLine")}
                    clearable
                    disabled={lineOptions.length === 0}
                    data={lineOptions}
                    {...form.getInputProps("billingLineId")}
                  />
                </Group>
                {values.projectId && (
                  <Switch label={t("billable")} {...form.getInputProps("billable", { type: "checkbox" })} />
                )}
                {values.billable && billing === undefined && (
                  <Text size="xs" c="dimmed">
                    {t("pricingIsTheProjects")}
                  </Text>
                )}
                {billing && (
                  <Stack gap={2} data-testid="expense-billing">
                    <Title order={6}>{t("billingHeading")}</Title>
                    <Text size="sm">{`${t("billAmount")}: ${format.money(billing.billAmount, currency)}`}</Text>
                    {billing.markupPercent !== undefined && (
                      <Text size="sm">
                        {`${t("markupPercent")}: ${t("vatPercent", { rate: format.number(billing.markupPercent, 0) })}`}
                      </Text>
                    )}
                    {billing.billRatePerKm !== undefined && (
                      <Text size="sm">{`${t("billRatePerKm")}: ${format.money(billing.billRatePerKm, currency)}`}</Text>
                    )}
                    {billing.invoice && (
                      <Text size="sm" c="dimmed">
                        {t("invoicedOn", { date: format.dateTime(billing.invoice.at) })}
                      </Text>
                    )}
                  </Stack>
                )}
              </Stack>
            )}
          </>
        )}

        {values.kind === "outlay" && (
          <>
            <Divider />
            <Stack gap="xs">
              <Title order={6}>{t("receipts")}</Title>
              {needsReceipt && (
                <Text size="sm" c="dimmed">
                  {t("receiptRequiredHint", {
                    amount: format.money(meta?.receiptRequiredOver ?? 0, currency),
                  })}
                </Text>
              )}
              <ReceiptThumbnails attachments={attachments} onRemove={(id) => removeReceipt.mutate(id)} />
              <ReceiptDropzone
                entryId={saved?.id}
                attachmentCount={attachments.length}
                onUploaded={(one) => {
                  setAttachments((current) => [...current, one]);
                  // The row's receipt count, the strip and the cached entry a
                  // re-open is seeded from all have to learn about it.
                  void queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
                }}
              />
            </Stack>
          </>
        )}

        <SimpleGrid cols={{ base: 1, sm: claim ? 2 : 3 }} spacing="sm">
          <Button type="button" variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {saved ? t("save") : t("saveDraft")}
          </Button>
          {/* A trip is submitted whole, lines and all, so a line inside one is
              never sent for approval on its own. */}
          {claim === undefined && (
            <Button type="button" variant="light" loading={save.isPending} onClick={() => submit(true)()}>
              {t("saveAndSubmit")}
            </Button>
          )}
        </SimpleGrid>
      </Stack>
    </form>
  );
};
