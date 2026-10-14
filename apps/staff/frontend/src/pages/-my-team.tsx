import { Alert, List, Stack, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, useI18n, useLocale } from "@vantigo/frontend-shell";
import { calendarQueryOptions, type StaffAbsence } from "../api/absence";
import { openCasesQueryOptions } from "../api/cases";
import type { StaffAbsenceType } from "../api/meta";
import { orgQueryOptions, type StaffPerson, type StaffPersonSummary } from "../api/people";
import { ApprovalsInbox } from "../components/approvals";
import { CaseCard } from "../components/case-card";
import { FieldGroup } from "../components/field-group";
import "../i18n";
import { absenceDays, absenceName } from "../lib/absence";
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
      <ApprovalsInbox types={types} />
      <MonthAbsence reports={org.data.reports} today={today} types={types} />
      <OpenCases reports={org.data.reports} />
    </Stack>
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
  const { t, date } = useStaffFormat();
  const cases = useQuery(openCasesQueryOptions());
  const names = new Map(reports.map((report) => [report.id, report.displayName]));
  const mine = cases.data?.data.filter((one) => names.has(one.personId)) ?? [];
  return (
    <FieldGroup title={t("openCasesTitle")} description={t("openCasesDescription")}>
      {cases.isPending && <ContentSkeleton rows={2} rowHeight={44} />}
      {cases.data && mine.length === 0 && <Text size="sm">{t("noOpenCases")}</Text>}
      {mine.map((sickCase) => (
        <CaseCard
          key={sickCase.id}
          sickCase={sickCase}
          title={t("caseLabel", { person: names.get(sickCase.personId) ?? "", date: date(sickCase.firstDay) })}
        />
      ))}
    </FieldGroup>
  );
};
