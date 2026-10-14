import { Alert, Text } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import type { StaffCareDays } from "../api/absence";
import { useStaffFormat } from "../lib/format";
import { FieldGroup, Fields } from "./field-group";

/**
 * A year's care days (omsorgsdager) as the server counted them (design S11):
 * shown only when the response carries them — the care-day fields' readers
 * alone are answered. Going over is shown, never refused.
 */
export const CareDaysCard = ({ careDays }: { careDays: StaffCareDays }) => {
  const { t, date } = useStaffFormat();
  return (
    <FieldGroup title={t("careDaysTitle", { year: careDays.year })} description={t("careDaysDescription")}>
      {careDays.recorded ? (
        <Fields
          items={[
            [t("careEntitlement"), String(careDays.entitlement)],
            [t("careUsed"), String(careDays.used)],
            [t("careRemaining"), String(careDays.remaining)],
            [t("careIneligibleDays"), careDays.ineligibleDays ? String(careDays.ineligibleDays) : undefined],
          ]}
        />
      ) : (
        <Text size="sm">{t("careDaysNotRecorded")}</Text>
      )}
      {!careDays.eligible && careDays.eligibleFrom && (
        <Text size="sm">{t("careEligibleFrom", { date: date(careDays.eligibleFrom) })}</Text>
      )}
      {careDays.exceeded && (
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
          {t("careExceeded")}
        </Alert>
      )}
    </FieldGroup>
  );
};
