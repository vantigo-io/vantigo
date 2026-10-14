import { Alert, Button, Card, Group, List, Modal, Stack, Text, Textarea } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCheck, IconPencil, IconX } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n, useLocale } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  approvalsQueryOptions,
  approveAbsence,
  calendarQueryOptions,
  rejectAbsence,
  type StaffAbsence,
  type StaffApproval,
} from "../api/absence";
import { openCasesQueryOptions, type StaffCase, updateAccommodationNote } from "../api/cases";
import type { StaffAbsenceType } from "../api/meta";
import { orgQueryOptions, type StaffPerson, type StaffPersonSummary } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import { FieldGroup, Fields } from "../components/field-group";
import { MilestoneList } from "../components/milestone-list";
import "../i18n";
import { absenceDays, absenceName, refusalText, useAbsenceDetails } from "../lib/absence";
import { toUtc } from "../lib/dates";
import { useStaffFormat } from "../lib/format";
import { monthOf } from "../lib/workdays";

/**
 * My team (design §7, plan reading 6): a section of My page for a caller who
 * manages anyone — the requests they decide, their reports' absence this
 * month and their reports' open sick-leave cases, every row as the server
 * shaped it for a manager (an HR-only type as "leave", no milestone note).
 * The reports are the organisation's answer for me today (`GET /people/{id}/org`).
 */
export const MyTeam = ({
  person,
  today,
  types,
}: {
  person: StaffPerson;
  today: string;
  types: readonly StaffAbsenceType[];
}) => {
  const { t } = useStaffFormat();
  const org = useQuery(orgQueryOptions(person.id));
  if (org.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadTeam")}>
        {org.error.message}
      </Alert>
    );
  if (!org.data) return <ContentSkeleton rows={4} rowHeight={44} />;
  return (
    <Stack gap="md">
      <Approvals types={types} />
      <MonthAbsence reports={org.data.reports} today={today} types={types} />
      <OpenCases reports={org.data.reports} />
    </Stack>
  );
};

/** The requests I decide, oldest first, each approved or rejected — with its reason — where it stands. */
const Approvals = ({ types }: { types: readonly StaffAbsenceType[] }) => {
  const { t } = useStaffFormat();
  const approvals = useQuery(approvalsQueryOptions());
  return (
    <FieldGroup title={t("approvalsTitle")} description={t("approvalsDescription")}>
      {approvals.isPending && <ContentSkeleton rows={2} rowHeight={44} />}
      {approvals.data?.data.length === 0 && <Text size="sm">{t("noApprovals")}</Text>}
      {approvals.data?.data.map((approval) => (
        <ApprovalRow key={approval.absence.id} approval={approval} types={types} />
      ))}
    </FieldGroup>
  );
};

const ApprovalRow = ({ approval, types }: { approval: StaffApproval; types: readonly StaffAbsenceType[] }) => {
  const { t, date } = useStaffFormat();
  const locale = useLocale();
  const queryClient = useQueryClient();
  const details = useAbsenceDetails();
  const [rejecting, setRejecting] = useState(false);
  const { absence, person } = approval;
  const name = absenceName(absence, types, locale, t);
  const label = t("approvalLabel", {
    person: person.displayName,
    absence: t("absenceLabel", { name, days: absenceDays(absence, date) }),
  });
  const form = useForm({
    initialValues: { reason: "" },
    validate: { reason: (value) => (value.trim() ? null : t("rejectReasonRequired")) },
  });
  const decided = async (title: string) => {
    notifications.show({ color: "teal", title, message: "" });
    await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
  };
  const failed = async (error: Error) => {
    notifications.show({ color: "red", title: t("couldNotDecide"), message: refusalText(error, t) });
    // Somebody quicker, or a withdrawn request: the inbox is read again.
    await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "approvals"] });
  };
  const approve = useMutation({
    mutationFn: () => approveAbsence(absence.id),
    onSuccess: () => decided(t("requestApproved")),
    onError: failed,
  });
  const reject = useMutation({
    mutationFn: (reason: string) => rejectAbsence(absence.id, reason),
    onSuccess: () => decided(t("requestRejected")),
    onError: failed,
  });

  return (
    <Card withBorder radius="md" padding="sm" component="article" aria-label={label}>
      <Stack gap={4}>
        <Text fw={500}>{person.displayName}</Text>
        <Text size="sm">{`${name} · ${absenceDays(absence, date)}`}</Text>
        {details(absence).map((detail) => (
          <Text key={detail} size="sm" c="dimmed">
            {detail}
          </Text>
        ))}
        {absence.note && <Text size="sm">{absence.note}</Text>}
        {rejecting ? (
          <form onSubmit={form.onSubmit((values) => reject.mutate(values.reason.trim()))}>
            <Stack gap="xs">
              <Textarea
                label={t("rejectReason")}
                description={t("rejectReasonDescription")}
                withAsterisk
                rows={3}
                maxLength={500}
                {...form.getInputProps("reason")}
              />
              <Group gap="xs">
                <Button type="submit" size="xs" color="red" loading={reject.isPending}>
                  {t("rejectRequest")}
                </Button>
                <Button type="button" size="xs" variant="default" onClick={() => setRejecting(false)}>
                  {t("cancel")}
                </Button>
              </Group>
            </Stack>
          </form>
        ) : (
          <Group gap="xs">
            <Button
              size="xs"
              leftSection={<IconCheck size={14} />}
              loading={approve.isPending}
              onClick={() => approve.mutate()}
            >
              {t("approve")}
            </Button>
            <Button
              size="xs"
              variant="light"
              color="red"
              leftSection={<IconX size={14} />}
              onClick={() => setRejecting(true)}
            >
              {t("reject")}
            </Button>
          </Group>
        )}
      </Stack>
    </Card>
  );
};

/**
 * My reports' absence in today's month, from the absence calendar — each
 * row as the server shaped it for me, a percentage or hours only when it
 * carries them.
 */
const MonthAbsence = ({
  reports,
  today,
  types,
}: {
  reports: StaffPersonSummary[];
  today: string;
  types: readonly StaffAbsenceType[];
}) => {
  const { t, date, number } = useStaffFormat();
  const { formatters } = useI18n("staff");
  const locale = useLocale();
  const month = monthOf(today);
  const calendar = useQuery(calendarQueryOptions(month.from, month.to));
  const title = t("absenceInMonth", {
    month: formatters.formatDate(toUtc(month.from), { month: "long", year: "numeric", timeZone: "UTC" }),
  });
  const rowsOf = (personId: number) => calendar.data?.people.find((one) => one.person.id === personId)?.absences ?? [];
  const line = (absence: StaffAbsence) =>
    [
      absenceName(absence, types, locale, t),
      absenceDays(absence, date),
      ...(absence.percent !== undefined && absence.percent < 100
        ? [t("absencePercent", { percent: number(absence.percent) })]
        : []),
      ...(absence.hours !== undefined ? [t("absenceHours", { hours: number(absence.hours) })] : []),
      ...(absence.status ? [t(`absenceStatus_${absence.status}`)] : []),
    ].join(" · ");
  return (
    <FieldGroup title={title} description={t("absenceInMonthDescription")}>
      {calendar.isPending && <ContentSkeleton rows={2} rowHeight={32} />}
      {calendar.data &&
        reports.map((report) => {
          const rows = rowsOf(report.id);
          return (
            <Stack key={report.id} gap={2}>
              <Text size="sm" fw={500}>
                {report.displayName}
              </Text>
              {rows.length === 0 ? (
                <Text size="sm" c="dimmed">
                  {t("noAbsenceThisMonth")}
                </Text>
              ) : (
                <List size="sm" aria-label={report.displayName}>
                  {rows.map((absence) => (
                    <List.Item key={absence.id}>{line(absence)}</List.Item>
                  ))}
                </List>
              )}
            </Stack>
          );
        })}
    </FieldGroup>
  );
};

/** My reports' open sick-leave cases with their milestones and the accommodation note. */
const OpenCases = ({ reports }: { reports: StaffPersonSummary[] }) => {
  const { t } = useStaffFormat();
  const cases = useQuery(openCasesQueryOptions());
  const names = new Map(reports.map((report) => [report.id, report.displayName]));
  const mine = cases.data?.data.filter((one) => names.has(one.personId)) ?? [];
  return (
    <FieldGroup title={t("openCasesTitle")} description={t("openCasesDescription")}>
      {cases.isPending && <ContentSkeleton rows={2} rowHeight={44} />}
      {cases.data && mine.length === 0 && <Text size="sm">{t("noOpenCases")}</Text>}
      {mine.map((sickCase) => (
        <CaseCard key={sickCase.id} sickCase={sickCase} name={names.get(sickCase.personId) ?? ""} />
      ))}
    </FieldGroup>
  );
};

const CaseCard = ({ sickCase, name }: { sickCase: StaffCase; name: string }) => {
  const { t, date } = useStaffFormat();
  const [editing, setEditing] = useState(false);
  return (
    <Card
      withBorder
      radius="md"
      padding="sm"
      component="article"
      aria-label={t("caseLabel", { person: name, date: date(sickCase.firstDay) })}
    >
      <Stack gap="xs">
        <Text fw={500}>{t("caseLabel", { person: name, date: date(sickCase.firstDay) })}</Text>
        <Fields
          items={[
            [t("caseLastDay"), date(sickCase.lastDay)],
            [t("caseMaxBenefit"), date(sickCase.maxBenefitEstimate)],
          ]}
        />
        <MilestoneList sickCase={sickCase} />
        <Stack gap={0}>
          <Text size="xs" c="dimmed">
            {t("accommodationNote")}
          </Text>
          {sickCase.accommodationNote && <Text size="sm">{sickCase.accommodationNote}</Text>}
        </Stack>
        <Group>
          <Button size="xs" variant="light" leftSection={<IconPencil size={14} />} onClick={() => setEditing(true)}>
            {t("changeAccommodationNote")}
          </Button>
        </Group>
      </Stack>
      <Modal opened={editing} onClose={() => setEditing(false)} title={t("accommodationNote")}>
        {editing && <AccommodationNoteForm sickCase={sickCase} onDone={() => setEditing(false)} />}
      </Modal>
    </Card>
  );
};

/** The accommodation note, sent with the case's revision; a newer save is a conflict, never overwritten. */
const AccommodationNoteForm = ({ sickCase, onDone }: { sickCase: StaffCase; onDone: () => void }) => {
  const { t } = useStaffFormat();
  const queryClient = useQueryClient();
  const form = useForm({ initialValues: { note: sickCase.accommodationNote ?? "" } });
  const save = useMutation({
    mutationFn: (note: string) => updateAccommodationNote(sickCase, note),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("accommodationNoteSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: async (error) => {
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalText(error, t) });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "cases"] });
    },
  });
  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values.note))}>
      <Stack>
        <Textarea
          label={t("accommodationNote")}
          description={t("accommodationNoteDescription")}
          rows={3}
          maxLength={2000}
          {...form.getInputProps("note")}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
