import { Button, Drawer, em, Group, Modal, NumberInput, Select, Stack, Text, Textarea } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useLocale } from "@vantigo/frontend-shell";
import {
  calendarQueryOptions,
  myHolidayAccountQueryOptions,
  mySelfCertificationQueryOptions,
  requestMyAbsence,
  type StaffAbsence,
  type StaffAbsenceInput,
  updateAbsence,
} from "../api/absence";
import type { StaffAbsenceType } from "../api/meta";
import { STAFF_QUERY_KEY } from "../api/request";
import { absenceRefusals, isSickKind } from "../lib/absence";
import { isRevisionConflict } from "../lib/errors";
import { useDecimalSeparator, useStaffFormat } from "../lib/format";
import { calendarWindows, workingDays } from "../lib/workdays";
import { SelfCertificationAllows } from "./self-certification";

export interface RequestSheetProps {
  opened: boolean;
  onClose: () => void;
  /** The absence types the caller may pick (`/meta`). */
  types: readonly StaffAbsenceType[];
  /** The business zone's day. */
  today: string;
  /** A request to change; left out, a new absence of my own. */
  absence?: StaffAbsence;
}

/**
 * The request sheet (design §7, phone first): a bottom sheet under `sm`, a
 * dialog above it. Its title names what it does; the form opens fresh each
 * time it is shown.
 */
export const RequestSheet = ({ opened, onClose, types, today, absence }: RequestSheetProps) => {
  const { t } = useStaffFormat();
  // Strictly below Mantine's `sm` (48em): at 768 px the sheet is already a dialog.
  const phone = useMediaQuery(`(max-width: ${em(767.98)})`);
  const title = absence ? t("changeAbsence") : t("requestAbsence");
  const form = opened && <RequestForm types={types} today={today} absence={absence} onDone={onClose} />;
  return phone ? (
    <Drawer opened={opened} onClose={onClose} title={title} position="bottom" size="90%">
      {form}
    </Drawer>
  ) : (
    <Modal opened={opened} onClose={onClose} title={title} size="lg">
      {form}
    </Modal>
  );
};

interface RequestFormValues {
  typeId: string | null;
  firstDay: string | null;
  lastDay: string | null;
  hours: number | string;
  percent: number | string;
  degreePercent: number | string;
  note: string;
}

/** The request's fields, as the form keys them and the request names them. */
const formFields = ["typeId", "firstDay", "lastDay", "hours", "percent", "degreePercent", "note"] as const;

const valuesOf = (absence: StaffAbsence | undefined, types: readonly StaffAbsenceType[]): RequestFormValues => {
  const type = types.find((one) => one.id === absence?.typeId);
  return {
    typeId: type ? String(type.id) : null,
    firstDay: absence?.firstDay ?? null,
    lastDay: absence && type?.unit === "days" ? absence.lastDay : null,
    hours: absence?.hours ?? "",
    percent: type?.allowsPercent && absence?.percent !== undefined ? absence.percent : "",
    degreePercent: absence?.degreePercent ?? "",
    note: absence?.note ?? "",
  };
};

/** The request as the type takes it: what the type has no field for is never sent. */
const inputOf = (values: RequestFormValues, type: StaffAbsenceType): StaffAbsenceInput => ({
  typeId: type.id,
  firstDay: values.firstDay as string,
  ...(type.unit === "days" && values.lastDay ? { lastDay: values.lastDay } : {}),
  ...(type.unit === "hours" ? { hours: Number(values.hours) } : {}),
  ...(type.allowsPercent && values.percent !== "" ? { percent: Number(values.percent) } : {}),
  ...(type.kind === "sick_certified" && values.degreePercent !== ""
    ? { degreePercent: Number(values.degreePercent) }
    : {}),
  ...(!isSickKind(type.kind) && values.note.trim() ? { note: values.note } : {}),
});

const RequestForm = ({
  types,
  today,
  absence,
  onDone,
}: {
  types: readonly StaffAbsenceType[];
  today: string;
  absence?: StaffAbsence;
  onDone: () => void;
}) => {
  const { t, number } = useStaffFormat();
  const locale = useLocale();
  const decimalSeparator = useDecimalSeparator();
  const queryClient = useQueryClient();
  const offered = types.filter((one) => !one.hidden && !one.reserved);
  const form = useForm<RequestFormValues>({
    initialValues: valuesOf(absence, offered),
    validate: {
      typeId: (value) => (value ? null : t("typeRequired")),
      firstDay: (value) => (value ? null : t("firstDayRequired")),
      // An hour type is one day: a last day a day type left behind is neither shown nor sent.
      lastDay: (value, values) =>
        offered.find((one) => String(one.id) === values.typeId)?.unit !== "hours" &&
        value &&
        values.firstDay &&
        value < values.firstDay
          ? t("lastDayBeforeFirst")
          : null,
      hours: (value, values) =>
        offered.find((one) => String(one.id) === values.typeId)?.unit === "hours" && value === ""
          ? t("hoursRequired")
          : null,
    },
  });
  const type = offered.find((one) => String(one.id) === form.values.typeId);

  const save = useMutation({
    mutationFn: (values: RequestFormValues) => {
      const input = inputOf(values, type as StaffAbsenceType);
      return absence ? updateAbsence(absence, input) : requestMyAbsence(input);
    },
    onSuccess: async (answer) => {
      // The server's answer, not the estimate: its working days and the
      // accounts after it, each year the row touches.
      const days =
        answer.workingDays === undefined || answer.hours !== undefined
          ? []
          : [
              answer.workingDays === 1
                ? t("answerOneWorkingDay")
                : t("answerWorkingDays", { count: answer.workingDays }),
            ];
      const accounts = (answer.accounts ?? []).map((account) =>
        t("answerAccount", {
          year: account.year,
          remaining: number(account.remaining),
          pending: number(account.pending),
        }),
      );
      notifications.show({
        color: "teal",
        title: absence
          ? t("absenceChanged")
          : answer.status === "requested"
            ? t("requestSent")
            : t("absenceRegistered"),
        message: [...days, ...accounts].join(" "),
      });
      if (answer.overdrawn) notifications.show({ color: "yellow", title: t("answerOverdrawn"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        onDone();
        return;
      }
      const { fields, rest } = absenceRefusals(error, formFields, t);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSendAbsence"), message: rest.join(" ") });
    },
  });

  const hourType = type?.unit === "hours";
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Select
          label={t("absenceType")}
          withAsterisk
          allowDeselect={false}
          data={offered.map((one) => ({ value: String(one.id), label: locale === "nb" ? one.names.nb : one.names.en }))}
          {...form.getInputProps("typeId")}
        />
        {type?.kind === "sick_self" && <SelfCertificationStanding today={today} />}
        <DateInput
          label={hourType ? t("oneDay") : t("firstDay")}
          withAsterisk
          valueFormat={t("dateInputFormat")}
          {...form.getInputProps("firstDay")}
        />
        {!hourType && (
          <DateInput
            label={t("lastDay")}
            description={t("lastDayDescription")}
            valueFormat={t("dateInputFormat")}
            clearable
            minDate={form.values.firstDay ?? undefined}
            {...form.getInputProps("lastDay")}
          />
        )}
        {hourType && (
          <NumberInput
            label={t("absenceHoursField")}
            description={t("absenceHoursDescription")}
            withAsterisk
            min={0.25}
            max={24}
            step={0.25}
            decimalScale={2}
            decimalSeparator={decimalSeparator}
            allowNegative={false}
            {...form.getInputProps("hours")}
          />
        )}
        {type?.allowsPercent && (
          <NumberInput
            label={t("percentOfDay")}
            description={t("percentOfDayDescription")}
            min={1}
            max={100}
            decimalScale={2}
            decimalSeparator={decimalSeparator}
            allowNegative={false}
            suffix=" %"
            {...form.getInputProps("percent")}
          />
        )}
        {type?.kind === "sick_certified" && (
          <NumberInput
            label={t("degree")}
            description={t("degreeDescription")}
            min={1}
            max={100}
            decimalScale={2}
            decimalSeparator={decimalSeparator}
            allowNegative={false}
            suffix=" %"
            {...form.getInputProps("degreePercent")}
          />
        )}
        {type && !isSickKind(type.kind) && (
          <Textarea label={t("absenceNote")} rows={2} maxLength={500} {...form.getInputProps("note")} />
        )}
        {type && !hourType && form.values.firstDay && (
          <Estimate
            type={type}
            firstDay={form.values.firstDay}
            lastDay={form.values.lastDay ?? form.values.firstDay}
            percent={form.values.percent === "" ? 100 : Number(form.values.percent)}
            absence={absence}
          />
        )}
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {absence ? t("save") : type?.needsDecision === false ? t("registerAbsence") : t("sendRequest")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/** What the policy allows me today, before an egenmelding is sent. */
const SelfCertificationStanding = ({ today }: { today: string }) => {
  const standing = useQuery(mySelfCertificationQueryOptions());
  return standing.data ? <SelfCertificationAllows standing={standing.data} today={today} /> : null;
};

/**
 * The working days of the range, Monday to Friday without the public
 * holidays `GET /calendar` answers for it (plan M13 — none worked out here),
 * and for a type drawing on the holiday account what is left after it. An
 * estimate: the server's count comes back with its answer.
 */
const Estimate = ({
  type,
  firstDay,
  lastDay,
  percent,
  absence,
}: {
  type: StaffAbsenceType;
  firstDay: string;
  lastDay: string;
  percent: number;
  absence?: StaffAbsence;
}) => {
  const { t, number } = useStaffFormat();
  const year = Number(firstDay.slice(0, 4));
  const valid = lastDay >= firstDay;
  const windows = valid ? calendarWindows(firstDay, lastDay) : [];
  const calendars = useQueries({ queries: windows.map(({ from, to }) => calendarQueryOptions(from, to)) });
  const account = useQuery({ ...myHolidayAccountQueryOptions(year), enabled: type.drawsHoliday });
  if (!valid || calendars.some((one) => !one.data)) return null;
  const holidays = calendars.flatMap((one) => one.data?.publicHolidays ?? []);
  const days = workingDays(firstDay, lastDay, holidays);
  const share = type.allowsPercent ? percent / 100 : 1;
  // A request being changed already counts among the pending days.
  const counted =
    absence?.status === "requested" && absence.typeId === type.id && absence.firstDay.slice(0, 4) === String(year)
      ? ((absence.workingDays ?? 0) * (absence.percent ?? 100)) / 100
      : 0;
  const after =
    type.drawsHoliday && account.data && lastDay.slice(0, 4) === String(year)
      ? account.data.remaining - account.data.pending + counted - days * share
      : undefined;
  return (
    <Stack gap={2}>
      <Text size="sm" fw={500}>
        {days === 1 ? t("estimateOneWorkingDay") : t("estimateWorkingDays", { count: days })}
      </Text>
      {after !== undefined && <Text size="sm">{t("estimateHolidayAfter", { days: number(after) })}</Text>}
      <Text size="xs" c="dimmed">
        {t("estimateExplained")}
      </Text>
    </Stack>
  );
};
