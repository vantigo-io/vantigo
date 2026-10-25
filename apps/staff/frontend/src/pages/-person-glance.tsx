import { Anchor, Card, SimpleGrid, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useI18n, useLocale } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { staffMetaQueryOptions } from "../api/meta";
import { personGlanceQueryOptions, type StaffGlanceAbsenceSpan, type StaffPerson } from "../api/people";
import { NotFoundError } from "../api/request";
import "../i18n";
import { absenceDays, absenceName } from "../lib/absence";
import { useStaffFormat } from "../lib/format";
import type { PersonTab } from "../lib/search";

interface GlanceCardProps {
  label: string;
  value?: ReactNode;
  hint?: ReactNode;
  /** The hint's colour when it warns — red for an overdrawn account. */
  color?: string;
  /** The tab the card expands on, linked from its label. */
  to?: PersonTab;
}

/**
 * One card: a figure, its label and a line under it. The figure is large
 * text rather than a heading, so the page's outline stays the groups'.
 */
const GlanceCard = ({ label, value, hint, color, to }: GlanceCardProps) => (
  <Card withBorder radius="md" padding="md" component="article" aria-label={label}>
    <Stack gap={4}>
      <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
        {to ? (
          <Anchor inherit renderRoot={(props) => <Link to="." search={{ tab: to }} {...props} />}>
            {label}
          </Anchor>
        ) : (
          label
        )}
      </Text>
      {value !== undefined && (
        <Text fz="h3" fw={700} lh={1.2} c={color}>
          {value}
        </Text>
      )}
      {hint && (
        <Text size="sm" c={color} component="div">
          {hint}
        </Text>
      )}
    </Stack>
  </Card>
);

/**
 * At a glance (person-glance design S4, §6): the cards `GET /people/{id}/glance`
 * answers this reader, one per section it carries — a section the server
 * left out is no card, never a zero. Nothing while it loads, and nothing on
 * a 404: a reader who sees the person always has a glance, so that is the
 * person gone. Any other failure is one dimmed line.
 */
export const PersonGlance = ({ person }: { person: StaffPerson }) => {
  const format = useStaffFormat();
  const t = format.t;
  const { formatters } = useI18n("staff");
  const locale = useLocale();
  const glance = useQuery(personGlanceQueryOptions(person.id));
  const meta = useQuery(staffMetaQueryOptions());
  if (glance.isError && !(glance.error instanceof NotFoundError))
    return (
      <Text size="sm" c="dimmed">
        {t("glanceError")}
      </Text>
    );
  if (!glance.data || !meta.data) return null;
  const { sickDays, selfCertification, holiday, childSick, employment, absence, contract, hours } = glance.data;
  const days = (value: number) => formatters.formatNumber(value, { maximumFractionDigits: 1 });
  const types = meta.data.absenceTypes;
  const span = (one: StaffGlanceAbsenceSpan) => ({
    what: absenceName(one, types, locale, t),
    dates: absenceDays(one, format.date),
  });
  // The Absence tab is there for whoever reads the absence detail — the same
  // reader person.tsx's holiday-account probe shows the tab to; keep the two in step.
  const absenceTab = holiday !== undefined || selfCertification !== undefined ? "absence" : undefined;

  const cards: ReactNode[] = [];
  if (sickDays)
    cards.push(
      <GlanceCard
        key="sickDays"
        label={t("glanceSickDays")}
        value={days(sickDays.selfCertified + sickDays.certified)}
        hint={t("glanceSickDaysHint", { self: days(sickDays.selfCertified), certified: days(sickDays.certified) })}
        to={absenceTab}
      />,
    );
  if (selfCertification) {
    const { occurrencesUsed, occurrencesLimit, daysUsed, daysLimit } = selfCertification;
    cards.push(
      <GlanceCard
        key="selfCertification"
        label={t("glanceSelfCert")}
        value={
          occurrencesLimit !== undefined
            ? t("glanceOfOccurrences", { used: occurrencesUsed, limit: occurrencesLimit })
            : daysLimit !== undefined
              ? t("glanceOfDays", { used: daysUsed, limit: daysLimit })
              : days(daysUsed)
        }
        hint={t("glanceLast12Months")}
        to={absenceTab}
      />,
    );
  }
  if (holiday)
    cards.push(
      <GlanceCard
        key="holiday"
        label={t("glanceHoliday")}
        value={
          holiday.remaining < 0
            ? t("glanceHolidayOverdrawn", { count: -holiday.remaining, days: days(-holiday.remaining) })
            : t("glanceHolidayLeft", { days: days(holiday.remaining) })
        }
        hint={t("glanceHolidayHint", { taken: days(holiday.takenToDate), planned: days(holiday.planned) })}
        color={holiday.overdrawn ? "red" : undefined}
        to={absenceTab}
      />,
    );
  if (childSick)
    cards.push(
      <GlanceCard
        key="childSick"
        label={t("glanceChildSick")}
        value={t("glanceDays", { count: childSick.days })}
        hint={
          childSick.entitlement !== undefined
            ? t("glanceOfEntitlement", { entitlement: childSick.entitlement })
            : t("glanceThisYear")
        }
        to={absenceTab}
      />,
    );
  if (employment && Object.keys(employment).length > 0) {
    const months = employment.tenureMonths;
    const tenure =
      months === undefined
        ? undefined
        : months < 12
          ? t("glanceTenureMonths", { count: months })
          : months % 12 === 0
            ? t("glanceTenureYears", { count: months / 12 })
            : t("glanceTenure", { years: Math.floor(months / 12), months: months % 12 });
    const lines = [
      employment.title,
      employment.startDate &&
        (tenure
          ? t("glanceSince", { date: format.date(employment.startDate), tenure })
          : format.date(employment.startDate)),
      employment.probationEndsOn && t("glanceProbationEnds", { date: format.date(employment.probationEndsOn) }),
    ].filter((line): line is string => Boolean(line));
    cards.push(
      <GlanceCard
        key="employment"
        label={t("glancePosition")}
        value={employment.ftePercent === undefined ? undefined : `${days(employment.ftePercent)} %`}
        hint={lines.length > 0 && lines.map((line) => <div key={line}>{line}</div>)}
        to="employments"
      />,
    );
  }
  if (absence) {
    const lines = [
      absence.now && t("glanceAway", span(absence.now)),
      absence.next && t("glanceNext", span(absence.next)),
      absence.openCase &&
        t("glanceCaseOpen", { date: format.date(absence.openCase.firstDay), day: absence.openCase.day }),
    ].filter((line): line is string => Boolean(line));
    cards.push(
      <GlanceCard
        key="absence"
        label={t("glanceAbsence")}
        hint={(lines.length > 0 ? lines : [t("glanceNothingPlanned")]).map((line) => <div key={line}>{line}</div>)}
        to={absenceTab}
      />,
    );
  }
  if (contract)
    cards.push(
      <GlanceCard
        key="contract"
        label={t("glanceContract")}
        value={
          contract.signed === null
            ? t("glanceNoEmployment")
            : contract.signed
              ? t("glanceSigned")
              : t("glanceNotSigned")
        }
        color={contract.signed === false ? "red" : undefined}
        to="documents"
      />,
    );
  if (hours)
    cards.push(
      <GlanceCard
        key="hours"
        label={t("glanceHours")}
        value={
          !hours.timeTracking
            ? undefined
            : hours.expected === undefined
              ? t("glanceHoursLogged", { logged: hours.logged === undefined ? "—" : format.number(hours.logged) })
              : t("glanceHoursOf", {
                  logged: hours.logged === undefined ? "—" : format.number(hours.logged),
                  expected: format.number(hours.expected),
                })
        }
        hint={
          !hours.timeTracking
            ? t("glanceTimeTrackingOff")
            : hours.logged === undefined
              ? t("glanceNoUserLinked")
              : t("glanceApproved", { hours: format.number(hours.approved ?? 0) })
        }
      />,
    );
  if (cards.length === 0) return null;

  return (
    <SimpleGrid role="group" aria-label={t("atAGlance")} cols={{ base: 2, sm: 3, lg: 4 }} spacing="sm">
      {cards}
    </SimpleGrid>
  );
};
