import { Button } from "@mantine/core";
import { IconCalendarPlus } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import type { JSX } from "react";
import "../i18n";
import { useCalendarNavigate } from "../lib/navigate";
import { CALENDAR_ROUTE_PATH } from "../lib/routes";

export interface NewAppointmentButtonProps {
  /** The customer the appointment is made from — the customer page's action. */
  customerId?: number;
  /** The project the appointment is made from — the project page's action. */
  projectId?: number;
  variant?: "default" | "light" | "subtle";
}

/**
 * "New appointment" for the host's record pages and the dashboard (design
 * D7): opens the calendar with a new appointment's sheet, the customer or
 * the project it is made from already filled in.
 */
export function NewAppointmentButton({
  customerId,
  projectId,
  variant = "default",
}: NewAppointmentButtonProps): JSX.Element {
  const { t } = useI18n("calendar");
  const navigate = useCalendarNavigate();
  return (
    <Button
      variant={variant}
      leftSection={<IconCalendarPlus size={16} />}
      onClick={() => void navigate({ to: CALENDAR_ROUTE_PATH, search: { new: 1, customerId, projectId } })}
    >
      {t("newAppointment")}
    </Button>
  );
}
