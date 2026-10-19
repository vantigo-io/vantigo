import {
  Alert,
  Badge,
  Button,
  Drawer,
  em,
  Group,
  Modal,
  MultiSelect,
  SegmentedControl,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { DateInput, TimeInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useDebouncedValue, useMediaQuery } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, civilDateIn, instantFromWallClock, useI18n, wallClockIn } from "@vantigo/frontend-shell";
import { type JSX, useState } from "react";
import {
  type Appointment,
  type AppointmentAnswer,
  type AppointmentTransparency,
  type AppointmentVisibility,
  type AppointmentWrite,
  appointmentQueryOptions,
  createAppointment,
  deleteAppointment,
  putOccurrence,
  REVISION_MISMATCH,
  respondToAppointment,
  updateAppointment,
} from "../api/appointments";
import { ApiValidationError, CALENDAR_QUERY_KEY } from "../api/request";
import { sessionUserIdQueryOptions } from "../api/session";
import { calendarUsersQueryOptions } from "../api/users";
import "../i18n";
import { addDays, civilDateValue, daysBetween } from "../lib/dates";
import { SEARCH_DEBOUNCE_MS } from "../lib/debounce";
import { formatRule, parseRule, type RecurrenceModel } from "../lib/recurrence";
import { type Span, shiftSeries, spanFields, spanOf } from "../lib/span";
import { type OccurrenceAnswer, OccurrenceQuestion } from "./occurrence-question";
import { RecurrenceControl } from "./recurrence-control";

export interface AppointmentSheetProps {
  opened: boolean;
  onClose: () => void;
  /** The appointment to show or change; left out, a new one. */
  appointmentId?: number;
  /** With `appointmentId`: the occurrence of its series the sheet was opened on. */
  occurrenceDate?: string;
  /** That occurrence's span as the grid drew it — a moved occurrence is not where the series would put it. */
  occurrenceSpan?: Span;
  /** A new appointment's span, from a selection on the grid. */
  span?: Span;
  /** The customer a new appointment is made from. */
  customerId?: number;
  /** The project a new appointment is made from. */
  projectId?: number;
}

/**
 * The appointment sheet (design D8): a drawer, or a full-screen dialog under
 * phone width. The owner creates and changes; an attendee reads and answers.
 */
export function AppointmentSheet(props: AppointmentSheetProps): JSX.Element {
  const { t } = useI18n("calendar");
  const { opened, onClose, appointmentId } = props;
  // Strictly below Mantine's `sm` (48em): at 768 px the sheet is already a drawer.
  const phone = useMediaQuery(`(max-width: ${em(767.98)})`);
  const title = appointmentId === undefined ? t("newAppointment") : t("appointment");
  // Keyed, so the form starts afresh for each appointment, occurrence or selection it is opened on.
  const key = `${appointmentId ?? "new"}:${props.occurrenceDate ?? ""}:${JSON.stringify(props.span ?? null)}`;
  const body =
    opened &&
    (appointmentId === undefined ? (
      <AppointmentForm key={key} {...props} />
    ) : (
      <ExistingAppointment key={key} {...props} id={appointmentId} />
    ));
  return phone ? (
    <Modal opened={opened} onClose={onClose} title={title} fullScreen>
      {body}
    </Modal>
  ) : (
    <Drawer opened={opened} onClose={onClose} title={title} position="right" size="md">
      {body}
    </Drawer>
  );
}

const ExistingAppointment = (props: AppointmentSheetProps & { id: number }) => {
  const { t } = useI18n("calendar");
  const appointment = useQuery(appointmentQueryOptions(props.id));
  if (appointment.isError)
    return (
      <Alert color="red" title={t("appointmentNotFound")}>
        {appointment.error.message}
      </Alert>
    );
  if (!appointment.data) return <ContentSkeleton rows={6} />;
  // The caller's own answer is there only when they are an attendee; the owner's is never stored.
  return appointment.data.myStatus === undefined ? (
    <AppointmentForm {...props} appointment={appointment.data} />
  ) : (
    <AttendeeView appointment={appointment.data} occurrenceSpan={occurrenceSpanOf(appointment.data, props)} />
  );
};

/** The span the sheet shows for an appointment: the occurrence it was opened on, or the appointment's own. */
const occurrenceSpanOf = (appointment: Appointment, props: AppointmentSheetProps): Span => {
  const series = spanOf(appointment);
  if (!props.occurrenceDate || !appointment.recurrence) return series;
  if (props.occurrenceSpan) return props.occurrenceSpan;
  const date = props.occurrenceDate;
  if (series.allDay)
    return { allDay: true, startDate: date, endDate: addDays(date, daysBetween(series.startDate, series.endDate)) };
  // The series puts each occurrence at the same wall-clock time in its own zone (D4).
  const start = instantFromWallClock(
    date,
    wallClockIn(appointment.timeZone, new Date(series.startsAt)),
    appointment.timeZone,
  );
  const duration = Date.parse(series.endsAt) - Date.parse(series.startsAt);
  return { allDay: false, startsAt: start.toISOString(), endsAt: new Date(start.getTime() + duration).toISOString() };
};

interface FormValues {
  title: string;
  allDay: boolean;
  startDate: string | null;
  endDate: string | null;
  startTime: string;
  endTime: string;
  location: string;
  description: string;
  visibility: AppointmentVisibility;
  transparency: AppointmentTransparency;
  recurrence: RecurrenceModel;
  attendees: string[];
}

/** The server's field names, as the form keys them. */
const fieldOf: Record<string, keyof FormValues> = {
  title: "title",
  allDay: "allDay",
  startDate: "startDate",
  endDate: "endDate",
  startsAt: "startTime",
  endsAt: "endTime",
  location: "location",
  description: "description",
  visibility: "visibility",
  transparency: "transparency",
  "recurrence.rule": "recurrence",
  recurrence: "recurrence",
  attendees: "attendees",
};

const formFieldOf = (serverField: string): keyof FormValues | undefined =>
  fieldOf[serverField] ?? fieldOf[serverField.replace(/\[\d+\]$/, "")];

const valuesOf = (span: Span, timeZone: string, appointment?: Appointment): FormValues => {
  const startDate = span.allDay ? span.startDate : civilDateIn(timeZone, new Date(span.startsAt));
  return {
    title: appointment?.title ?? "",
    allDay: span.allDay,
    startDate,
    endDate: span.allDay ? span.endDate : civilDateIn(timeZone, new Date(span.endsAt)),
    startTime: span.allDay ? "09:00" : wallClockIn(timeZone, new Date(span.startsAt)),
    endTime: span.allDay ? "10:00" : wallClockIn(timeZone, new Date(span.endsAt)),
    location: appointment?.location ?? "",
    description: appointment?.description ?? "",
    visibility: appointment?.visibility ?? "shared",
    transparency: appointment?.transparency ?? "busy",
    recurrence: parseRule(
      appointment?.recurrence?.rule,
      appointment ? spanStartDate(spanOf(appointment), timeZone) : startDate,
    ),
    attendees: appointment?.attendees.map((attendee) => attendee.user.id) ?? [],
  };
};

const spanStartDate = (span: Span, timeZone: string) =>
  span.allDay ? span.startDate : civilDateIn(timeZone, new Date(span.startsAt));

/** The form's dates and wall-clock times as a span: the instants they mean in the writer's zone. */
const spanOfValues = (values: FormValues, timeZone: string): Span | null => {
  if (!values.startDate) return null;
  const endDate = values.endDate ?? values.startDate;
  if (values.allDay) return { allDay: true, startDate: values.startDate, endDate };
  if (!values.startTime || !values.endTime) return null;
  return {
    allDay: false,
    startsAt: instantFromWallClock(values.startDate, values.startTime, timeZone).toISOString(),
    endsAt: instantFromWallClock(endDate, values.endTime, timeZone).toISOString(),
  };
};

const defaultSpan = (timeZone: string): Span => {
  const today = civilDateIn(timeZone);
  return {
    allDay: false,
    startsAt: instantFromWallClock(today, "09:00", timeZone).toISOString(),
    endsAt: instantFromWallClock(today, "10:00", timeZone).toISOString(),
  };
};

type SaveRequest = { kind: "create" | "update" | "series" | "occurrence"; span: Span; values: FormValues };

const AppointmentForm = ({
  appointment,
  occurrenceDate,
  occurrenceSpan,
  span: newSpan,
  customerId,
  projectId,
  onClose,
}: AppointmentSheetProps & { appointment?: Appointment }) => {
  const { t, timeZone } = useI18n("calendar");
  const queryClient = useQueryClient();
  const shownSpan = appointment
    ? occurrenceSpanOf(appointment, { occurrenceDate, occurrenceSpan, opened: true, onClose })
    : (newSpan ?? defaultSpan(timeZone));
  const recurring = Boolean(appointment?.recurrence && occurrenceDate);
  const [question, setQuestion] = useState<"save" | "delete" | null>(null);
  const [pending, setPending] = useState<{ span: Span; values: FormValues } | null>(null);

  const form = useForm<FormValues>({
    initialValues: valuesOf(shownSpan, timeZone, appointment),
    validate: {
      title: (value) => (value.trim() ? null : t("titleRequired")),
      startDate: (value) => (value ? null : t("startRequired")),
      endDate: (value, values) =>
        values.allDay && value && values.startDate && value < values.startDate ? t("endBeforeStart") : null,
      endTime: (value, values) => {
        if (values.allDay) return null;
        const span = spanOfValues(values, timeZone);
        if (!value || !span) return t("timeRequired");
        return !span.allDay && Date.parse(span.endsAt) <= Date.parse(span.startsAt) ? t("endBeforeStart") : null;
      },
    },
  });

  const done = async (title: string) => {
    notifications.show({ color: "teal", title, message: "" });
    await queryClient.invalidateQueries({ queryKey: [CALENDAR_QUERY_KEY] });
    onClose();
  };

  const refused = async (error: Error) => {
    if ((error as { code?: string }).code === REVISION_MISMATCH) {
      notifications.show({ color: "yellow", title: t("appointmentChanged"), message: t("appointmentChangedHint") });
      await queryClient.invalidateQueries({ queryKey: [CALENDAR_QUERY_KEY] });
      onClose();
      return;
    }
    if (error instanceof ApiValidationError) {
      const fields: Record<string, string> = {};
      const rest: string[] = [];
      for (const [field, messages] of Object.entries(error.errors)) {
        const key = formFieldOf(field);
        if (key) fields[key] = messages[0];
        else rest.push(messages[0]);
      }
      form.setErrors(fields);
      if (rest.length === 0) return;
      notifications.show({ color: "red", title: t("couldNotSave"), message: rest.join(" ") });
      return;
    }
    notifications.show({ color: "red", title: t("couldNotSave"), message: error.message });
  };

  const writeOf = (values: FormValues, span: Span): AppointmentWrite => {
    const rule = formatRule(values.recurrence);
    const customer = appointment ? appointment.customer?.id : customerId;
    const project = appointment ? appointment.project?.id : projectId;
    return {
      title: values.title.trim(),
      allDay: span.allDay,
      ...spanFields(span),
      timeZone,
      visibility: values.visibility,
      transparency: values.transparency,
      ...(values.location.trim() ? { location: values.location.trim() } : {}),
      ...(values.description.trim() ? { description: values.description.trim() } : {}),
      ...(rule ? { recurrence: { rule } } : {}),
      attendees: [...new Set(values.attendees)],
      ...(customer !== undefined ? { customerId: customer } : {}),
      ...(project !== undefined ? { projectId: project } : {}),
    };
  };

  const save = useMutation({
    mutationFn: async ({ kind, span, values }: SaveRequest) => {
      if (kind === "create") return createAppointment(writeOf(values, span));
      const current = appointment as Appointment;
      if (kind === "occurrence") return putOccurrence(current.id, occurrenceDate as string, spanFields(span));
      const seriesSpan = kind === "series" ? shiftSeries(spanOf(current), shownSpan, span) : span;
      return updateAppointment(current.id, current.revision, writeOf(values, seriesSpan));
    },
    onSuccess: (_answer, { kind }) => done(kind === "create" ? t("appointmentCreated") : t("appointmentSaved")),
    onError: refused,
  });

  const remove = useMutation({
    mutationFn: (answer: OccurrenceAnswer) => {
      const current = appointment as Appointment;
      return answer === "occurrence"
        ? putOccurrence(current.id, occurrenceDate as string, { cancelled: true }).then(() => undefined)
        : deleteAppointment(current.id);
    },
    onSuccess: () => done(t("appointmentDeleted")),
    onError: (error: Error) => notifications.show({ color: "red", title: t("couldNotDelete"), message: error.message }),
  });

  const submit = form.onSubmit((values) => {
    const span = spanOfValues(values, timeZone);
    if (!span) return;
    if (!appointment) return save.mutate({ kind: "create", span, values });
    if (recurring) {
      setPending({ span, values });
      setQuestion("save");
      return;
    }
    save.mutate({ kind: "update", span, values });
  });

  const askDelete = () => {
    if (recurring) {
      setQuestion("delete");
      return;
    }
    modals.openConfirmModal({
      title: t("deleteAppointmentTitle"),
      children: <Text size="sm">{t("deleteAppointmentConfirm", { title: appointment?.title ?? "" })}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      zIndex: 400,
      onConfirm: () => remove.mutate("series"),
    });
  };

  const answer = (choice: OccurrenceAnswer) => {
    const action = question;
    setQuestion(null);
    if (action === "delete") return remove.mutate(choice);
    if (pending) save.mutate({ kind: choice, ...pending });
  };

  const customerLabel = appointment
    ? appointment.customer && (appointment.customer.name ?? t("customerNumber", { id: appointment.customer.id }))
    : customerId !== undefined && t("customerNumber", { id: customerId });
  const projectLabel = appointment
    ? appointment.project &&
      ([appointment.project.code, appointment.project.name].filter(Boolean).join(" · ") ||
        t("projectNumber", { id: appointment.project.id }))
    : projectId !== undefined && t("projectNumber", { id: projectId });

  const busy = save.isPending || remove.isPending;
  return (
    <form onSubmit={submit} noValidate>
      <Stack>
        <TextInput label={t("title")} withAsterisk maxLength={200} data-autofocus {...form.getInputProps("title")} />
        {(customerLabel || projectLabel) && (
          <Group gap="xs">
            {customerLabel && (
              <Badge variant="light" color="blue">
                {t("fromCustomer", { name: customerLabel })}
              </Badge>
            )}
            {projectLabel && (
              <Badge variant="light" color="grape">
                {t("fromProject", { name: projectLabel })}
              </Badge>
            )}
          </Group>
        )}
        <Switch label={t("allDay")} {...form.getInputProps("allDay", { type: "checkbox" })} />
        <Group grow align="flex-start">
          <DateInput
            label={form.values.allDay ? t("firstDay") : t("startDate")}
            withAsterisk
            valueFormat={t("dateInputFormat")}
            {...form.getInputProps("startDate")}
          />
          {!form.values.allDay && (
            <TimeInput label={t("startTime")} withAsterisk {...form.getInputProps("startTime")} />
          )}
        </Group>
        <Group grow align="flex-start">
          <DateInput
            label={form.values.allDay ? t("lastDay") : t("endDate")}
            withAsterisk
            valueFormat={t("dateInputFormat")}
            minDate={form.values.startDate ?? undefined}
            {...form.getInputProps("endDate")}
          />
          {!form.values.allDay && <TimeInput label={t("endTime")} withAsterisk {...form.getInputProps("endTime")} />}
        </Group>
        {!form.values.allDay && (
          <Text size="xs" c="dimmed">
            {t("timesInZone", { zone: timeZone })}
          </Text>
        )}
        <TextInput label={t("location")} maxLength={200} {...form.getInputProps("location")} />
        <Textarea label={t("description")} rows={3} maxLength={10000} {...form.getInputProps("description")} />
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {t("visibility")}
          </Text>
          <SegmentedControl
            aria-label={t("visibility")}
            data={[
              { value: "shared", label: t("visibilityShared") },
              { value: "private", label: t("visibilityPrivate") },
            ]}
            {...form.getInputProps("visibility")}
          />
        </Stack>
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {t("transparency")}
          </Text>
          <SegmentedControl
            aria-label={t("transparency")}
            data={[
              { value: "busy", label: t("transparencyBusy") },
              { value: "free", label: t("transparencyFree") },
            ]}
            {...form.getInputProps("transparency")}
          />
        </Stack>
        <RecurrenceControl
          value={form.values.recurrence}
          onChange={(recurrence) => form.setFieldValue("recurrence", recurrence)}
          error={typeof form.errors.recurrence === "string" ? form.errors.recurrence : undefined}
        />
        <AttendeePicker
          value={form.values.attendees}
          onChange={(attendees) => form.setFieldValue("attendees", attendees)}
          ownerId={appointment?.owner.id}
          known={appointment?.attendees.map((attendee) => attendee.user) ?? []}
          error={typeof form.errors.attendees === "string" ? form.errors.attendees : undefined}
        />
        <Group justify="space-between">
          {appointment ? (
            <Button type="button" variant="subtle" color="red" onClick={askDelete} disabled={busy}>
              {t("delete")}
            </Button>
          ) : (
            <span />
          )}
          <Group gap="xs">
            <Button type="button" variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={save.isPending} disabled={remove.isPending}>
              {t("save")}
            </Button>
          </Group>
        </Group>
      </Stack>
      <OccurrenceQuestion
        opened={question !== null}
        action={question ?? "save"}
        onAnswer={answer}
        onCancel={() => setQuestion(null)}
      />
    </form>
  );
};

/**
 * The colleagues on an appointment, picked from the directory as the person
 * types. The owner is never offered — the server refuses them — and the
 * names already on it stay whatever the search narrows to.
 */
const AttendeePicker = ({
  value,
  onChange,
  ownerId,
  known,
  error,
}: {
  value: string[];
  onChange: (value: string[]) => void;
  ownerId?: string;
  known: readonly { id: string; displayName: string }[];
  error?: string;
}) => {
  const { t } = useI18n("calendar");
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, SEARCH_DEBOUNCE_MS);
  const users = useQuery(calendarUsersQueryOptions(debounced));
  const me = useQuery(sessionUserIdQueryOptions());
  const owner = ownerId ?? me.data ?? undefined;
  const [names, setNames] = useState<Map<string, string>>(
    () => new Map(known.map((user) => [user.id, user.displayName])),
  );
  const options = new Map(names);
  for (const user of users.data ?? []) if (user.id !== owner) options.set(user.id, user.displayName);
  return (
    <MultiSelect
      label={t("attendees")}
      placeholder={value.length === 0 ? t("searchColleagues") : undefined}
      searchable
      clearable
      searchValue={search}
      onSearchChange={setSearch}
      filter={({ options: parsed }) => parsed}
      nothingFoundMessage={t("noColleaguesFound")}
      data={[...options].map(([id, displayName]) => ({ value: id, label: displayName }))}
      value={value}
      error={error}
      onChange={(ids) => {
        setNames((current) => {
          const next = new Map(current);
          for (const id of ids) if (options.has(id)) next.set(id, options.get(id) as string);
          return next;
        });
        onChange([...new Set(ids)].filter((id) => id !== owner));
      }}
    />
  );
};

const answers: readonly { status: AppointmentAnswer; key: string; color: string }[] = [
  { status: "accepted", key: "accept", color: "teal" },
  { status: "tentative", key: "tentative", color: "yellow" },
  { status: "declined", key: "decline", color: "red" },
];

const statusKeys: Record<string, string> = {
  "needs-action": "statusNeedsAction",
  accepted: "statusAccepted",
  tentative: "statusTentative",
  declined: "statusDeclined",
};

/** What an attendee sees: the appointment, read-only, and the three answers. */
const AttendeeView = ({ appointment, occurrenceSpan }: { appointment: Appointment; occurrenceSpan: Span }) => {
  const { t, formatters } = useI18n("calendar");
  const queryClient = useQueryClient();
  const respond = useMutation({
    mutationFn: (status: AppointmentAnswer) => respondToAppointment(appointment.id, status),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("answerSent"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [CALENDAR_QUERY_KEY] });
    },
    onError: (error: Error) => notifications.show({ color: "red", title: t("couldNotAnswer"), message: error.message }),
  });
  const when = occurrenceSpan.allDay
    ? occurrenceSpan.startDate === occurrenceSpan.endDate
      ? formatters.formatDate(civilDateValue(occurrenceSpan.startDate), { dateStyle: "full", timeZone: "UTC" })
      : `${formatters.formatDate(civilDateValue(occurrenceSpan.startDate), { dateStyle: "medium", timeZone: "UTC" })} – ${formatters.formatDate(civilDateValue(occurrenceSpan.endDate), { dateStyle: "medium", timeZone: "UTC" })}`
    : `${formatters.formatDate(occurrenceSpan.startsAt, { dateStyle: "full", timeStyle: "short" })} – ${formatters.formatDate(occurrenceSpan.endsAt, { timeStyle: "short" })}`;
  return (
    <Stack>
      <Text fw={600} size="lg">
        {appointment.title}
      </Text>
      <Text size="sm">{when}</Text>
      {appointment.location && <Text size="sm">{t("locationValue", { location: appointment.location })}</Text>}
      {appointment.description && (
        <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
          {appointment.description}
        </Text>
      )}
      <Text size="sm" c="dimmed">
        {t("organiser", { name: appointment.owner.displayName })}
      </Text>
      <Stack gap={2}>
        <Text size="sm" fw={500}>
          {t("attendees")}
        </Text>
        {appointment.attendees.map((attendee) => (
          <Text size="sm" key={attendee.user.id}>
            {attendee.user.displayName} · {t(statusKeys[attendee.status])}
          </Text>
        ))}
      </Stack>
      <Text size="sm" fw={500}>
        {t("yourAnswer", { status: t(statusKeys[appointment.myStatus ?? "needs-action"]) })}
      </Text>
      <Group gap="xs">
        {answers.map(({ status, key, color }) => (
          <Button
            key={status}
            color={color}
            variant={appointment.myStatus === status ? "filled" : "light"}
            loading={respond.isPending && respond.variables === status}
            disabled={respond.isPending}
            onClick={() => respond.mutate(status)}
          >
            {t(key)}
          </Button>
        ))}
      </Group>
    </Stack>
  );
};
