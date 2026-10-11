import { Badge, Button, Card, Group, List, Stack, Text, Timeline } from "@mantine/core";
import { IconPencil, IconTrash } from "@tabler/icons-react";
import type { ReactNode } from "react";
import type { StaffEmployment, StaffTerms } from "../api/employments";
import type { StaffStructure } from "../api/meta";
import "../i18n";
import { useStaffFormat } from "../lib/format";
import { inForceOn } from "../lib/terms";
import { type FieldItem, Fields } from "./field-group";

export interface TermsTimelineProps {
  /** The employment the rows belong to: no row is in force outside its days. */
  employment: Pick<StaffEmployment, "startDate" | "endDate">;
  /** The chain as the server answers it, oldest first. */
  terms: StaffTerms[];
  /**
   * The person reading their own terms (My page): HR's note on a row is left
   * off whatever the response carries — an HR user reading themselves gets
   * it from the server as HR, but My page is what they see of themselves.
   */
  asSelf?: boolean;
  /** The business zone's day, which decides the row in force. */
  today: string;
  structure?: StaffStructure;
  /** A name for a person a row names as manager. */
  personName: (id: number) => string;
  /** A name for a department the structure does not hold for this reader. */
  departmentName?: (id: number) => string | undefined;
  /** A name for a location the structure does not hold for this reader. */
  locationName?: (id: number) => string | undefined;
  /**
   * Whether the reader may see the pay (the person's `canSeePay`): the pay is
   * drawn only then, whatever a row carries.
   */
  canSeePay: boolean;
  /** HR's actions on a row; left out, the timeline only reads. */
  onCorrect?: (row: StaffTerms) => void;
  onDelete?: (row: StaffTerms) => void;
}

/**
 * An employment's terms as a timeline, newest first (design §7): each row
 * with its days and every field the response carries for this reader — a
 * colleague's rows hold the type and the percentage alone, a manager's no
 * pay — and the row in force today marked. HR corrects any row and deletes
 * the latest, never the first.
 */
export const TermsTimeline = ({
  employment,
  terms,
  asSelf = false,
  today,
  structure,
  personName,
  departmentName,
  locationName,
  canSeePay,
  onCorrect,
  onDelete,
}: TermsTimelineProps) => {
  const format = useStaffFormat();
  const t = format.t;
  const newestFirst = [...terms].reverse();
  const latest = terms[terms.length - 1];
  const first = terms[0];

  const label = (key: string, value: string | undefined) => (value ? t(`${key}_${value}`) : undefined);
  const date = (value?: string) => (value ? format.date(value) : undefined);
  const number = (value?: number) => (value === undefined ? undefined : format.number(value));
  const department = (id?: number) =>
    id === undefined ? undefined : (structure?.departments.find((row) => row.id === id)?.name ?? departmentName?.(id));
  const location = (id?: number) =>
    id === undefined ? undefined : (structure?.locations.find((row) => row.id === id)?.name ?? locationName?.(id));
  const payOnly = <T,>(value: T) => (canSeePay ? value : undefined);

  const fieldsOf = (row: StaffTerms): FieldItem[] => [
    [t("employmentType"), label("employmentType", row.employmentType)],
    [t("ftePercent"), t("percentValue", { value: format.number(row.ftePercent) })],
    [t("appointmentForm"), label("appointmentForm", row.appointmentForm)],
    [t("temporaryGround"), label("temporaryGround", row.temporaryGround)],
    [t("expectedEndDate"), date(row.expectedEndDate)],
    [t("hiredOutTo"), row.hiredOutTo],
    [t("workingHoursScheme"), label("workingHoursScheme", row.workingHoursScheme)],
    [t("fullTimeHoursPerWeek"), number(row.fullTimeHoursPerWeek)],
    [t("jobTitle"), row.title],
    [t("occupationCode"), row.occupationCode],
    [t("department"), department(row.departmentId)],
    [t("location"), location(row.locationId)],
    [t("manager"), row.managerPersonId === undefined ? undefined : personName(row.managerPersonId)],
    [t("employeeType"), label("employeeType", row.employeeType)],
    [t("costCentre"), row.costCentre],
    [t("division"), row.division],
    [t("probationEndDate"), date(row.probationEndDate)],
    [t("probationExtendedTo"), row.probationExtensionAgreed ? date(row.probationExtendedTo) : undefined],
    [t("probationExtensionReason"), row.probationExtensionAgreed ? row.probationExtensionReason : undefined],
    [t("noticeEmployee"), number(row.noticeEmployee)],
    [t("noticeEmployer"), number(row.noticeEmployer)],
    [t("noticeUnit"), label("noticeUnit", row.noticeUnit)],
    [t("workingTimeText"), row.workingTimeText],
    [t("breakMinutes"), number(row.breakMinutes)],
    [t("specialHoursText"), row.specialHoursText],
    [t("overtimeRuleText"), row.overtimeRuleText],
    [t("tariffReference"), row.tariffReference],
    [t("holidayTermsText"), row.holidayTermsText],
    [t("competenceDevelopmentText"), row.competenceDevelopmentText],
    [t("socialSecurityText"), row.socialSecurityText],
    [t("remunerationType"), payOnly(label("remunerationType", row.remunerationType))],
    [t("payMethod"), payOnly(row.payMethod)],
    [t("paydayText"), payOnly(row.paydayText)],
    [t("seniorityDate"), payOnly(date(row.seniorityDate))],
    [t("payGrade"), payOnly(row.payGrade)],
    [t("termsNote"), asSelf ? undefined : row.note],
  ];

  const pay = (row: StaffTerms): ReactNode =>
    canSeePay && row.payComponents && row.payComponents.length > 0 ? (
      <Stack gap={2}>
        <Text size="xs" c="dimmed">
          {t("payComponents")}
        </Text>
        <List size="sm" spacing={2}>
          {row.payComponents.map((component, index) => (
            <List.Item key={index}>
              {t("payComponentLine", {
                name: component.name,
                amount: format.number(component.amount),
                currency: component.currency,
                period: t(`payPeriod_${component.period}`),
              })}
            </List.Item>
          ))}
        </List>
      </Stack>
    ) : null;

  return (
    <Timeline bulletSize={14} lineWidth={2} active={-1}>
      {newestFirst.map((row) => {
        const name = row.validTo
          ? t("termsFromTo", { from: format.date(row.validFrom), to: format.date(row.validTo) })
          : t("termsFrom", { date: format.date(row.validFrom) });
        const current = inForceOn(row, today, employment);
        const deletable = onDelete && row.id === latest?.id && row.id !== first?.id;
        return (
          <Timeline.Item key={row.id} color={current ? "teal" : "gray"}>
            <Card component="section" aria-label={name} withBorder padding="sm" radius="sm">
              <Stack gap="xs">
                <Group justify="space-between" wrap="wrap" gap="xs">
                  <Group gap="xs">
                    <Text size="sm" fw={600}>
                      {row.validTo
                        ? t("periodFromTo", { from: format.date(row.validFrom), to: format.date(row.validTo) })
                        : t("periodFrom", { date: format.date(row.validFrom) })}
                    </Text>
                    {current && (
                      <Badge size="sm" color="teal" variant="light">
                        {t("inForce")}
                      </Badge>
                    )}
                  </Group>
                  {(onCorrect || deletable) && (
                    <Group gap={4}>
                      {onCorrect && (
                        <Button
                          size="compact-sm"
                          variant="subtle"
                          leftSection={<IconPencil size={14} />}
                          onClick={() => onCorrect(row)}
                        >
                          {t("correctTerms")}
                        </Button>
                      )}
                      {deletable && (
                        <Button
                          size="compact-sm"
                          variant="subtle"
                          color="red"
                          leftSection={<IconTrash size={14} />}
                          onClick={() => onDelete(row)}
                        >
                          {t("deleteTerms")}
                        </Button>
                      )}
                    </Group>
                  )}
                </Group>
                <Fields items={fieldsOf(row)} />
                {pay(row)}
              </Stack>
            </Card>
          </Timeline.Item>
        );
      })}
    </Timeline>
  );
};
