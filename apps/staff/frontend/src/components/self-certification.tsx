import { Alert, Text } from "@mantine/core";
import { IconAlertCircle, IconInfoCircle } from "@tabler/icons-react";
import type { ReactNode } from "react";
import type { StaffSelfCertification } from "../api/absence";
import { useStaffFormat } from "../lib/format";
import { FieldGroup, Fields } from "./field-group";

/**
 * A withdrawal of the right to egenmelding (ftrl. § 8-27) as the server sent
 * it: its start only when the response carries one.
 */
const Withdrawal = ({ standing, own = true }: { standing: StaffSelfCertification; own?: boolean }) => {
  const { t, date } = useStaffFormat();
  if (!standing.withdrawnUntil) return null;
  const values = { until: date(standing.withdrawnUntil), reason: standing.withdrawnReason ?? "" };
  const prefix = own ? "selfCert" : "theirSelfCert";
  return (
    <Alert color="orange" icon={<IconAlertCircle size={16} />}>
      {standing.withdrawnFrom
        ? t(`${prefix}WithdrawnFromUntil`, { ...values, from: date(standing.withdrawnFrom) })
        : t(`${prefix}WithdrawnUntil`, values)}
    </Alert>
  );
};

/**
 * My standing under the self-certification policy today, as the server
 * judged it (design S10): the limits by the policy's name — occurrences
 * under the minimum, days under the extended policy, both under custom
 * numbers, each only when the response carries it — and what I used in the
 * rolling year. On the person page (`own` false) it speaks of the person,
 * with what HR may do beside it.
 */
export const SelfCertificationCard = ({
  standing,
  own = true,
  actions,
}: {
  standing: StaffSelfCertification;
  own?: boolean;
  actions?: ReactNode;
}) => {
  const { t, date } = useStaffFormat();
  return (
    <FieldGroup
      title={t("selfCertTitle")}
      description={t(own ? "selfCertDescription" : "theirSelfCertDescription")}
      actions={actions}
    >
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
      <Withdrawal standing={standing} own={own} />
    </FieldGroup>
  );
};

/** What the policy allows today, shown in the request sheet before an egenmelding is sent. */
export const SelfCertificationAllows = ({
  standing,
  today,
  own = true,
}: {
  standing: StaffSelfCertification;
  today: string;
  own?: boolean;
}) => {
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
            {t(own ? "selfCertUsedTimes" : "theirSelfCertUsedTimes", {
              used: standing.occurrencesUsed,
              limit: standing.occurrencesPerYear,
            })}
          </Text>
        )}
        {standing.daysPerYear !== undefined && (
          <Text size="sm">
            {t(own ? "selfCertUsedDays" : "theirSelfCertUsedDays", {
              used: standing.daysUsed,
              limit: standing.daysPerYear,
            })}
          </Text>
        )}
        {standing.eligibleFrom && standing.eligibleFrom > today && (
          <Text size="sm">{t(own ? "selfCertFrom" : "theirSelfCertFrom", { date: date(standing.eligibleFrom) })}</Text>
        )}
      </Alert>
      <Withdrawal standing={standing} own={own} />
    </>
  );
};
