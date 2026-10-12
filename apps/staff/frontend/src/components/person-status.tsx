import { Badge } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
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

/**
 * Beside an ended status: the retention run has erased the person (design
 * S15). It is no state of its own — an erased person is a leaver whose
 * personnel administration is gone — so it is a second, outlined badge.
 */
export const ErasedBadge = () => {
  const { t } = useI18n("staff");
  return (
    <Badge variant="outline" color="gray">
      {t("erasedBadge")}
    </Badge>
  );
};
