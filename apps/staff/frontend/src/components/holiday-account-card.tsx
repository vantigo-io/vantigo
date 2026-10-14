import { Alert } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import type { StaffHolidayAccount } from "../api/absence";
import { useStaffFormat } from "../lib/format";
import { FieldGroup, Fields } from "./field-group";

/**
 * A holiday account for one year as the server counted it (design §4), in
 * working days: the entitlement, what was carried in — automatically only when
 * the server says so — taken, requested and still waiting, and what remains.
 * Overdrawn is a warning, never a refusal.
 */
export const HolidayAccountCard = ({ account }: { account: StaffHolidayAccount }) => {
  const { t, number } = useStaffFormat();
  return (
    <FieldGroup title={t("holidayAccountTitle", { year: account.year })} description={t("holidayAccountDescription")}>
      <Fields
        items={[
          [t("holidayEntitlement"), number(account.entitlement)],
          [t("holidayCarriedIn"), number(account.carriedIn)],
          [t("holidayCarriedInDerived"), account.carriedInDerived ? number(account.carriedInDerived) : undefined],
          [t("holidayTaken"), number(account.taken)],
          [t("holidayPending"), number(account.pending)],
          [t("holidayRemaining"), number(account.remaining)],
        ]}
      />
      {account.overdrawn && (
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
          {t("holidayOverdrawn")}
        </Alert>
      )}
    </FieldGroup>
  );
};
