import { Badge, type MantineSize } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";
import { type TimeEntryStatus, timeEntryStatusColor, timeEntryStatusLabelKey } from "../lib/status";

export interface EntryStatusBadgeProps {
  status: TimeEntryStatus;
  size?: MantineSize;
}

/** A time entry's status, in the one colour and wording every time view uses for it. */
export const EntryStatusBadge = ({ status, size }: EntryStatusBadgeProps) => {
  const { t } = useI18n("time");
  return (
    <Badge variant="light" color={timeEntryStatusColor(status)} size={size}>
      {t(timeEntryStatusLabelKey(status))}
    </Badge>
  );
};
