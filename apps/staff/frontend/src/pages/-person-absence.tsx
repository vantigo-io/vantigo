import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Modal,
  NumberInput,
  Select,
  Stack,
  Text,
  Textarea,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconChevronLeft, IconChevronRight, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useLocale } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  absenceIfMatch,
  cancelAbsence,
  carryOver,
  createHolidayAdjustment,
  deleteHolidayAdjustment,
  holidayAdjustmentsQueryOptions,
  personAbsencesQueryOptions,
  personCareDaysQueryOptions,
  personHolidayAccountQueryOptions,
  personSelfCertificationQueryOptions,
  postponeHoliday,
  type StaffAbsence,
  type StaffHolidayAdjustment,
  type StaffSelfCertification,
  setSelfCertWithdrawal,
} from "../api/absence";
import { personCasesQueryOptions } from "../api/cases";
import type { StaffAbsenceType, StaffCapabilities } from "../api/meta";
import type { StaffPerson } from "../api/people";
import { ApiValidationError, STAFF_QUERY_KEY } from "../api/request";
import { CareDaysCard } from "../components/care-days-card";
import { CaseCard } from "../components/case-card";
import { FieldGroup } from "../components/field-group";
import { HolidayAccountCard } from "../components/holiday-account-card";
import { RequestSheet } from "../components/request-sheet";
import { SelfCertificationCard } from "../components/self-certification";
import "../i18n";
import { absenceDays, absenceName, refusalText, useAbsenceDetails } from "../lib/absence";
import { useDecimalSeparator, useStaffFormat } from "../lib/format";

export interface PersonAbsenceProps {
  person: StaffPerson;
  today: string;
  /** The absence types `/meta` lists. */
  types: readonly StaffAbsenceType[];
  /** The caller's capabilities (`/meta`): `staff:personal-data` writes the milestones' notes. */
  capabilities: StaffCapabilities;
}

/** What one person's Absence tab offers the caller, from the capabilities the server answered. */
interface Offers {
  /** staff:manage (not on an erased record): adjustments, carry-over, rows, the withdrawal, the case's estimate and close. */
  hr: boolean;
  /** A decider of the person's absence: cancels a decided row. */
  decides: boolean;
  /** The person looking at their own page: reads, changes nothing here. */
  self: boolean;
}

/**
 * The person page's Absence tab (design §7, plan Task 7c): the holiday
 * account a year at a time with HR's adjustments and carry-over, the
 * egenmelding standing and its withdrawal, the care days when the server
 * answers them, the open sick-leave case with its milestones, and the
 * person's absences — HR's *Change* of a request or a registration,
 * *Correct* of an approved row, a decider's *Cancel absence*, and HR's
 * *Postpone holiday* on a sykmelding. Every row and figure is drawn as the
 * server shaped it for the caller; a write sends the token the row carries.
 */
export const PersonAbsence = ({ person, today, types, capabilities }: PersonAbsenceProps) => {
  const { t, date } = useStaffFormat();
  const thisYear = Number(today.slice(0, 4));
  const [year, setYear] = useState(thisYear);
  const offers: Offers = {
    hr: person.capabilities.canEdit,
    decides: person.capabilities.canDecide,
    self: person.capabilities.canSeeHealth && !capabilities.canSeeHealth,
  };
  const account = useQuery(personHolidayAccountQueryOptions(person.id, year));
  const standing = useQuery(personSelfCertificationQueryOptions(person.id));
  const careDays = useQuery(personCareDaysQueryOptions(person.id, thisYear));
  const cases = useQuery(personCasesQueryOptions(person.id));
  const absences = useQuery(personAbsencesQueryOptions(person.id));
  const [sheet, setSheet] = useState<{ absence?: StaffAbsence; correct?: boolean } | undefined>();
  const [carrying, setCarrying] = useState(false);
  const [postponing, setPostponing] = useState<StaffAbsence | undefined>();

  return (
    <Stack gap="md">
      <Group justify="space-between" wrap="wrap" gap="xs">
        <Group gap={4}>
          <ActionIcon variant="subtle" aria-label={t("previousYear")} onClick={() => setYear(year - 1)}>
            <IconChevronLeft size={16} />
          </ActionIcon>
          <Text fw={500}>{year}</Text>
          <ActionIcon variant="subtle" aria-label={t("nextYear")} onClick={() => setYear(year + 1)}>
            <IconChevronRight size={16} />
          </ActionIcon>
        </Group>
        {offers.hr && (
          <Button variant="light" onClick={() => setCarrying(true)}>
            {t("carryOverFrom", { year })}
          </Button>
        )}
      </Group>
      {account.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadAbsence")}>
          {account.error.message}
        </Alert>
      )}
      {account.data ? <HolidayAccountCard account={account.data} /> : account.isPending && <ContentSkeleton rows={3} />}
      {offers.hr && <Adjustments personId={person.id} year={year} />}
      {standing.data && (
        <SelfCertificationCard
          standing={standing.data}
          own={false}
          actions={offers.hr && <WithdrawalActions personId={person.id} standing={standing.data} today={today} />}
        />
      )}
      {careDays.data && <CareDaysCard careDays={careDays.data} />}
      {cases.data?.data.map((sickCase) => (
        <CaseCard
          key={sickCase.id}
          sickCase={sickCase}
          title={t("caseFrom", { date: date(sickCase.firstDay) })}
          canTick={!offers.self}
          canChangeNote={
            !offers.self &&
            (offers.hr ||
              capabilities.canSeeHealth ||
              sickCase.accommodationNote !== undefined ||
              (offers.decides && capabilities.managesAnyone))
          }
          hr={offers.hr}
          canWriteNotes={capabilities.canSeeHealth}
        />
      ))}
      <FieldGroup
        title={t("personAbsences")}
        actions={
          offers.hr && (
            <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setSheet({})}>
              {t("registerAbsenceTitle")}
            </Button>
          )
        }
      >
        {absences.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {absences.error.message}
          </Alert>
        )}
        {absences.isPending && <ContentSkeleton rows={3} rowHeight={44} />}
        {absences.data?.data.length === 0 && <EmptyState title={t("noPersonAbsences")} />}
        {absences.data?.data.map((absence) => (
          <AbsenceRow
            key={absence.id}
            absence={absence}
            types={types}
            offers={offers}
            onChange={() => setSheet({ absence })}
            onCorrect={() => setSheet({ absence, correct: true })}
            onPostpone={() => setPostponing(absence)}
          />
        ))}
      </FieldGroup>
      <RequestSheet
        opened={sheet !== undefined}
        onClose={() => setSheet(undefined)}
        types={types}
        today={today}
        absence={sheet?.absence}
        correct={sheet?.correct}
        personId={person.id}
      />
      <CarryOverModal personId={person.id} year={year} opened={carrying} onClose={() => setCarrying(false)} />
      <PostponeModal absence={postponing} today={today} onClose={() => setPostponing(undefined)} />
    </Stack>
  );
};

/** One of the person's absences as the server shaped it, with what the caller may do to it. */
const AbsenceRow = ({
  absence,
  types,
  offers,
  onChange,
  onCorrect,
  onPostpone,
}: {
  absence: StaffAbsence;
  types: readonly StaffAbsenceType[];
  offers: Offers;
  onChange: () => void;
  onCorrect: () => void;
  onPostpone: () => void;
}) => {
  const { t, date } = useStaffFormat();
  const locale = useLocale();
  const queryClient = useQueryClient();
  const describe = useAbsenceDetails();
  const name = absenceName(absence, types, locale, t);
  const days = absenceDays(absence, date);
  const type = types.find((one) => one.id === absence.typeId);
  // A write names the row's type and sends its token: a row shown as leave,
  // or one carrying no token, is not the caller's to change.
  const writable = absence.shownAs === "type" && absenceIfMatch(absence) !== undefined;
  const status = absence.status;
  const canChange = offers.hr && writable && (status === "requested" || status === "registered");
  const canCorrect = offers.hr && writable && status === "approved";
  const canCancel =
    absence.shownAs === "type" &&
    ((status === "approved" && (offers.hr || offers.decides)) || (status === "requested" && offers.hr));
  const canPostpone =
    offers.hr &&
    type?.kind === "sick_certified" &&
    (absence.degreePercent ?? 100) === 100 &&
    (status === "registered" || status === "approved");
  const cancel = useMutation({
    mutationFn: () => cancelAbsence(absence.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("absenceCancelled"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: async (error) => {
      notifications.show({ color: "red", title: t("couldNotCancelAbsence"), message: refusalText(error, t) });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
  });
  const confirmCancel = () =>
    modals.openConfirmModal({
      title: t("cancelAbsenceTitle"),
      children: <Text size="sm">{t("cancelAbsenceExplained")}</Text>,
      labels: { confirm: t("cancelTheAbsence"), cancel: t("keepTheAbsence") },
      confirmProps: { color: "red" },
      onConfirm: () => cancel.mutate(),
    });

  return (
    <Card withBorder radius="md" padding="sm" component="article" aria-label={t("absenceLabel", { name, days })}>
      <Stack gap={4}>
        <Group justify="space-between" wrap="wrap" gap="xs">
          <Text fw={500}>{name}</Text>
          {status && <Badge variant="light">{t(`absenceStatus_${status}`)}</Badge>}
        </Group>
        <Text size="sm">{days}</Text>
        {describe(absence).map((detail) => (
          <Text key={detail} size="sm" c="dimmed">
            {detail}
          </Text>
        ))}
        {absence.note && <Text size="sm">{absence.note}</Text>}
        {absence.decisionNote && (
          <Stack gap={0}>
            <Text size="xs" c="dimmed">
              {t("decisionNote")}
            </Text>
            <Text size="sm">{absence.decisionNote}</Text>
          </Stack>
        )}
        {(canChange || canCorrect || canCancel || canPostpone) && (
          <Group gap="xs" wrap="wrap">
            {canChange && (
              <Button size="xs" variant="light" onClick={onChange}>
                {t("changeAbsenceButton")}
              </Button>
            )}
            {canCorrect && (
              <Button size="xs" variant="light" onClick={onCorrect}>
                {t("correctAbsenceButton")}
              </Button>
            )}
            {canPostpone && (
              <Button size="xs" variant="light" onClick={onPostpone}>
                {t("postponeHoliday")}
              </Button>
            )}
            {canCancel && (
              <Button size="xs" variant="subtle" color="red" loading={cancel.isPending} onClick={confirmCancel}>
                {t("cancelAbsence")}
              </Button>
            )}
          </Group>
        )}
      </Stack>
    </Card>
  );
};

/** HR's adjustments of the person's holiday account (staff:manage): listed, added for the year shown, deleted. */
const Adjustments = ({ personId, year }: { personId: number; year: number }) => {
  const { t, number } = useStaffFormat();
  const queryClient = useQueryClient();
  const adjustments = useQuery(holidayAdjustmentsQueryOptions(personId));
  const [adding, setAdding] = useState(false);
  const remove = useMutation({
    mutationFn: (adjustment: StaffHolidayAdjustment) => deleteHolidayAdjustment(adjustment.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("adjustmentDeleted"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotSave"), message: refusalText(error, t) }),
  });
  const confirmRemove = (adjustment: StaffHolidayAdjustment) =>
    modals.openConfirmModal({
      title: t("deleteAdjustmentTitle"),
      children: <Text size="sm">{t("deleteAdjustmentExplained")}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(adjustment),
    });
  const line = (adjustment: StaffHolidayAdjustment) =>
    [
      t("adjustmentLine", {
        year: adjustment.year,
        days: `${adjustment.days > 0 ? "+" : ""}${number(adjustment.days)}`,
      }),
      adjustment.fromYear === undefined
        ? t(`adjustmentReason_${adjustment.reason}`)
        : t(`adjustmentReasonFrom_${adjustment.reason}`, { year: adjustment.fromYear }),
      adjustment.note,
    ]
      .filter(Boolean)
      .join(" · ");
  return (
    <FieldGroup
      title={t("holidayAdjustments")}
      description={t("holidayAdjustmentsDescription")}
      actions={
        <Button size="xs" variant="light" leftSection={<IconPlus size={14} />} onClick={() => setAdding(true)}>
          {t("addAdjustment")}
        </Button>
      }
    >
      {adjustments.isPending && <ContentSkeleton rows={2} rowHeight={32} />}
      {adjustments.data?.length === 0 && <Text size="sm">{t("noAdjustments")}</Text>}
      {adjustments.data?.map((adjustment) => (
        <Group key={adjustment.id} justify="space-between" wrap="nowrap" gap="xs">
          <Text size="sm">{line(adjustment)}</Text>
          <ActionIcon
            variant="subtle"
            color="red"
            aria-label={t("deleteAdjustment")}
            onClick={() => confirmRemove(adjustment)}
          >
            <IconTrash size={16} />
          </ActionIcon>
        </Group>
      ))}
      <Modal opened={adding} onClose={() => setAdding(false)} title={t("addAdjustment")}>
        {adding && <AdjustmentForm personId={personId} year={year} onDone={() => setAdding(false)} />}
      </Modal>
    </FieldGroup>
  );
};

interface AdjustmentValues {
  reason: StaffHolidayAdjustment["reason"];
  days: number | string;
  fromYear: number | string;
  note: string;
}

const AdjustmentForm = ({ personId, year, onDone }: { personId: number; year: number; onDone: () => void }) => {
  const { t } = useStaffFormat();
  const decimalSeparator = useDecimalSeparator();
  const queryClient = useQueryClient();
  const form = useForm<AdjustmentValues>({
    initialValues: { reason: "correction", days: "", fromYear: year - 1, note: "" },
    validate: {
      days: (value) =>
        value === "" || Number(value) === 0 || Math.abs(Number(value)) > 366 ? t("adjustmentDaysInvalid") : null,
      fromYear: (value, values) =>
        values.reason !== "correction" && (value === "" || Number(value) >= year)
          ? t("fromYearBefore", { year })
          : null,
      note: (value, values) => (values.reason !== "agreed_carry_over" && !value.trim() ? t("sayWhy") : null),
    },
  });
  const save = useMutation({
    mutationFn: (values: AdjustmentValues) =>
      createHolidayAdjustment(personId, {
        year,
        days: Number(values.days),
        reason: values.reason,
        ...(values.reason === "correction" ? {} : { fromYear: Number(values.fromYear) }),
        ...(values.note.trim() ? { note: values.note.trim() } : {}),
      }),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("adjustmentAdded"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const errors = error.fieldErrors;
        form.setErrors(Object.fromEntries(Object.entries(errors).filter(([field]) => field in form.values)));
        if (!Object.keys(errors).some((field) => field in form.values)) form.setErrors({ days: error.message });
        return;
      }
      form.setErrors({ days: refusalText(error, t) });
    },
  });
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Text size="sm" c="dimmed">
          {t("adjustmentForYear", { year })}
        </Text>
        <Select
          label={t("adjustmentReason")}
          allowDeselect={false}
          data={(["correction", "agreed_carry_over", "forced_carry_over"] as const).map((value) => ({
            value,
            label: t(`adjustmentReason_${value}`),
          }))}
          {...form.getInputProps("reason")}
        />
        <NumberInput
          label={t("adjustmentDays")}
          description={t("adjustmentDaysDescription")}
          withAsterisk
          decimalScale={1}
          decimalSeparator={decimalSeparator}
          min={-366}
          max={366}
          {...form.getInputProps("days")}
        />
        {form.values.reason !== "correction" && (
          <NumberInput
            label={t("fromYear")}
            allowDecimal={false}
            thousandSeparator={false}
            {...form.getInputProps("fromYear")}
          />
        )}
        <Textarea
          label={t("adjustmentNote")}
          withAsterisk={form.values.reason !== "agreed_carry_over"}
          rows={2}
          maxLength={500}
          {...form.getInputProps("note")}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("add")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/** Carries `year`'s untaken holiday into the next: agreed within the scheme's maximum, or forced with a note. */
const CarryOverModal = ({
  personId,
  year,
  opened,
  onClose,
}: {
  personId: number;
  year: number;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t } = useStaffFormat();
  return (
    <Modal opened={opened} onClose={onClose} title={t("carryOverTitle", { year })}>
      {opened && <CarryOverForm personId={personId} year={year} onDone={onClose} />}
    </Modal>
  );
};

const CarryOverForm = ({ personId, year, onDone }: { personId: number; year: number; onDone: () => void }) => {
  const { t } = useStaffFormat();
  const decimalSeparator = useDecimalSeparator();
  const queryClient = useQueryClient();
  const [refused, setRefused] = useState<string | null>(null);
  const form = useForm<{ kind: "agreed" | "forced"; days: number | string; note: string }>({
    initialValues: { kind: "agreed", days: "", note: "" },
    validate: {
      days: (value) => (value !== "" && Number(value) <= 0 ? t("carryOverDaysInvalid") : null),
      note: (value, values) => (values.kind === "forced" && !value.trim() ? t("sayWhy") : null),
    },
  });
  const save = useMutation({
    mutationFn: (values: { kind: "agreed" | "forced"; days: number | string; note: string }) =>
      carryOver(personId, {
        fromYear: year,
        kind: values.kind,
        ...(values.days === "" ? {} : { days: Number(values.days) }),
        ...(values.note.trim() ? { note: values.note.trim() } : {}),
      }),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("carriedOver", { year }), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) => setRefused(refusalText(error, t)),
  });
  return (
    <form
      onSubmit={form.onSubmit((values) => {
        setRefused(null);
        save.mutate(values);
      })}
    >
      <Stack>
        <Text size="sm">{t("carryOverExplained", { year, next: year + 1 })}</Text>
        {refused && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {refused}
          </Alert>
        )}
        <Select
          label={t("carryOverKind")}
          allowDeselect={false}
          data={[
            { value: "agreed", label: t("carryOverKind_agreed") },
            { value: "forced", label: t("carryOverKind_forced") },
          ]}
          {...form.getInputProps("kind")}
        />
        <NumberInput
          label={t("adjustmentDays")}
          description={t("carryOverDaysDescription")}
          decimalScale={1}
          decimalSeparator={decimalSeparator}
          allowNegative={false}
          {...form.getInputProps("days")}
        />
        <Textarea
          label={t("adjustmentNote")}
          withAsterisk={form.values.kind === "forced"}
          rows={2}
          maxLength={500}
          {...form.getInputProps("note")}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("carryOver")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/** HR withdraws the right to egenmelding (ftrl. § 8-27) until a day, or lifts a withdrawal the standing carries. */
const WithdrawalActions = ({
  personId,
  standing,
  today,
}: {
  personId: number;
  standing: StaffSelfCertification;
  today: string;
}) => {
  const { t } = useStaffFormat();
  const queryClient = useQueryClient();
  const [withdrawing, setWithdrawing] = useState(false);
  const lift = useMutation({
    mutationFn: () => setSelfCertWithdrawal(personId, { until: null }),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("withdrawalLifted"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotSave"), message: refusalText(error, t) }),
  });
  const confirmLift = () =>
    modals.openConfirmModal({
      title: t("liftWithdrawalTitle"),
      children: <Text size="sm">{t("liftWithdrawalExplained")}</Text>,
      labels: { confirm: t("liftIt"), cancel: t("cancel") },
      onConfirm: () => lift.mutate(),
    });
  return (
    <Group gap="xs">
      <Button size="xs" variant="light" onClick={() => setWithdrawing(true)}>
        {t("withdrawSelfCert")}
      </Button>
      {standing.withdrawnUntil && (
        <Button size="xs" variant="subtle" loading={lift.isPending} onClick={confirmLift}>
          {t("liftWithdrawal")}
        </Button>
      )}
      <Modal opened={withdrawing} onClose={() => setWithdrawing(false)} title={t("withdrawSelfCert")}>
        {withdrawing && (
          <WithdrawalForm personId={personId} standing={standing} today={today} onDone={() => setWithdrawing(false)} />
        )}
      </Modal>
    </Group>
  );
};

/** The day a year after `day`. */
const aYearAfter = (day: string) => `${Number(day.slice(0, 4)) + 1}${day.slice(4)}`;

const WithdrawalForm = ({
  personId,
  standing,
  today,
  onDone,
}: {
  personId: number;
  standing: StaffSelfCertification;
  today: string;
  onDone: () => void;
}) => {
  const { t } = useStaffFormat();
  const queryClient = useQueryClient();
  const form = useForm<{ until: string | null; reason: string }>({
    initialValues: { until: standing.withdrawnUntil ?? null, reason: standing.withdrawnReason ?? "" },
    validate: {
      until: (value) => (!value || value <= today || value > aYearAfter(today) ? t("withdrawUntilInvalid") : null),
      reason: (value) => (value.trim() ? null : t("sayWhy")),
    },
  });
  const save = useMutation({
    mutationFn: (values: { until: string | null; reason: string }) =>
      setSelfCertWithdrawal(personId, { until: values.until, reason: values.reason.trim() }),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("selfCertWithdrawn"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) => form.setErrors({ reason: refusalText(error, t) }),
  });
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Text size="sm">{t("withdrawSelfCertExplained")}</Text>
        <DateInput
          label={t("withdrawUntil")}
          withAsterisk
          valueFormat={t("dateInputFormat")}
          {...form.getInputProps("until")}
        />
        <Textarea label={t("withdrawReason")} withAsterisk rows={2} maxLength={500} {...form.getInputProps("reason")} />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("withdraw")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/**
 * HR records the employee's demand to postpone approved ferie for a 100 %
 * sykmelding (ferieloven § 9): the ferie the sykmelding lies over is split
 * and its days return to the account. The refusals in the reader's words.
 */
const PostponeModal = ({ absence, today, onClose }: { absence?: StaffAbsence; today: string; onClose: () => void }) => {
  const { t } = useStaffFormat();
  const phone = useMediaQuery("(max-width: 48em)");
  return (
    <Modal opened={absence !== undefined} onClose={onClose} title={t("postponeHoliday")} fullScreen={phone}>
      {absence && <PostponeForm absence={absence} today={today} onDone={onClose} />}
    </Modal>
  );
};

const PostponeForm = ({ absence, today, onDone }: { absence: StaffAbsence; today: string; onDone: () => void }) => {
  const { t } = useStaffFormat();
  const queryClient = useQueryClient();
  const form = useForm<{ demandedOn: string | null }>({
    initialValues: { demandedOn: today },
    validate: { demandedOn: (value) => (value && value > today ? t("demandedOnAfterToday") : null) },
  });
  const save = useMutation({
    mutationFn: (values: { demandedOn: string | null }) => postponeHoliday(absence.id, values.demandedOn ?? undefined),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("holidayPostponed"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) =>
      form.setErrors({
        demandedOn:
          error instanceof ApiValidationError && "demandedOn" in error.fieldErrors
            ? t("demandedBeforeSickness")
            : refusalText(error, t),
      }),
  });
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Text size="sm">{t("postponeHolidayExplained")}</Text>
        <Text size="sm">{t("postponeHolidayDuring")}</Text>
        <DateInput
          label={t("demandedOn")}
          description={t("demandedOnDescription")}
          valueFormat={t("dateInputFormat")}
          maxDate={today}
          {...form.getInputProps("demandedOn")}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("recordDemand")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
