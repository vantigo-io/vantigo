import { Badge } from "@mantine/core";
import type { StaffPersonStatus } from "../api/people";
import "../i18n";
import { useStaffFormat } from "../lib/format";

const colors: Record<StaffPersonStatus["state"], string> = {
  active: "green",
  upcoming: "blue",
  ended: "gray",
  none: "yellow",
};

/**
 * Where a person stands today in the business zone (design §7): active, the
 * day an upcoming employment starts, the day the last one ended, or no
 * employment yet — the half-finished two-step hire, kept findable so HR can
 * add the employment.
 */
export const PersonStatusBadge = ({ status }: { status: StaffPersonStatus }) => {
  const format = useStaffFormat();
  const t = format.t;
  const text =
    status.state === "upcoming" && status.date
      ? t("statusStartsOn", { date: format.date(status.date) })
      : status.state === "ended" && status.date
        ? t("statusEndedOn", { date: format.date(status.date) })
        : status.state === "active"
          ? t("statusActive")
          : status.state === "none"
            ? t("statusNone")
            : t(status.state === "upcoming" ? "statusUpcoming" : "statusEnded");
  return (
    <Badge variant="light" color={colors[status.state]}>
      {text}
    </Badge>
  );
};
