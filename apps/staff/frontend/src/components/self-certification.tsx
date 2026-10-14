import { Alert, Text } from "@mantine/core";
import { IconAlertCircle, IconInfoCircle } from "@tabler/icons-react";
import type { StaffSelfCertification } from "../api/absence";
import { useStaffFormat } from "../lib/format";
import { FieldGroup, Fields } from "./field-group";

/**
 * A withdrawal of the right to egenmelding (ftrl. § 8-27) as the server sent
 * it: its start only when the response carries one.
 */
const Withdrawal = ({ standing }: { standing: StaffSelfCertification }) => {
  const { t, date } = useStaffFormat();
  if (!standing.withdrawnUntil) return null;
  const values = { until: date(standing.withdrawnUntil), reason: standing.withdrawnReason ?? "" };
  return (
    <Alert color="orange" icon={<IconAlertCircle size={16} />}>
      {standing.withdrawnFrom
        ? t("selfCertWithdrawnFromUntil", { ...values, from: date(standing.withdrawnFrom) })
        : t("selfCertWithdrawnUntil", values)}
    </Alert>
  );
};

/**
 * My standing under the self-certification policy today, as the server
 * judged it (design S10): the limits by the policy's name — occurrences
 * under the minimum, days under the extended policy, both under custom
 * numbers, each only when the response carries it — and what I used in the
 * rolling year.
 */
export const SelfCertificationCard = ({ standing }: { standing: StaffSelfCertification }) => {
  const { t, date } = useStaffFormat();
  return (
    <FieldGroup title={t("selfCertTitle")} description={t("selfCertDescription")}>
      <Fields
        items={[
          [t("selfCertPolicy"), t(`selfCertPolicy_${standing.policy}`)],
          [t("selfCertAtATime"), t("selfCertUpToDays", { count: standing.daysPerOccurrence })],
          [
            t("selfCertOccurrences"),
            standing.occurrencesPerYear === undefined
              ? undefined
              : t("selfCertUsedOf", { used: standing.occurrencesUsed, limit: standing.occurrencesPerYear }),
          ],
          [
            t("selfCertDays"),
            standing.daysPerYear === undefined
              ? String(standing.daysUsed)
              : t("selfCertUsedOf", { used: standing.daysUsed, limit: standing.daysPerYear }),
          ],
          [t("selfCertEligibleFrom"), standing.eligibleFrom ? date(standing.eligibleFrom) : undefined],
        ]}
      />
      <Withdrawal standing={standing} />
    </FieldGroup>
  );
};

/** What the policy allows today, shown in the request sheet before an egenmelding is sent. */
export const SelfCertificationAllows = ({ standing, today }: { standing: StaffSelfCertification; today: string }) => {
  const { t, date } = useStaffFormat();
  const limits = [
    t("selfCertAllowsAtATime", { count: standing.daysPerOccurrence }),
    ...(standing.occurrencesPerYear === undefined
      ? []
      : [t("selfCertAllowsTimes", { count: standing.occurrencesPerYear })]),
    ...(standing.daysPerYear === undefined ? [] : [t("selfCertAllowsDays", { count: standing.daysPerYear })]),
  ];
  return (
    <>
      <Alert
        role="status"
        color="blue"
        icon={<IconInfoCircle size={16} />}
        title={t(`selfCertPolicy_${standing.policy}`)}
      >
        <Text size="sm">{`${limits.join(", ")}.`}</Text>
        {standing.occurrencesPerYear !== undefined && (
          <Text size="sm">
            {t("selfCertUsedTimes", { used: standing.occurrencesUsed, limit: standing.occurrencesPerYear })}
          </Text>
        )}
        {standing.daysPerYear !== undefined && (
          <Text size="sm">{t("selfCertUsedDays", { used: standing.daysUsed, limit: standing.daysPerYear })}</Text>
        )}
        {standing.eligibleFrom && standing.eligibleFrom > today && (
          <Text size="sm">{t("selfCertFrom", { date: date(standing.eligibleFrom) })}</Text>
        )}
      </Alert>
      <Withdrawal standing={standing} />
    </>
  );
};
