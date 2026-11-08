import { useI18n } from "@vantigo/frontend-shell";
import { errorCode } from "../api/request";
import { heldKey, type Remaining } from "../lib/review";
import { useErrorText } from "./use-error-text";

/** A countdown's remaining time, in the viewer's language. */
export const useRemainingText = () => {
  const { t } = useI18n("agents");
  return (left: Remaining): string => {
    switch (left.kind) {
      case "daysHours":
        return t("durationDaysHours", { days: left.days, hours: left.hours });
      case "hoursMinutes":
        return t("durationHoursMinutes", { hours: left.hours, minutes: left.minutes });
      case "minutes":
        return t("durationMinutes", { minutes: left.minutes });
      default:
        return t("durationLessThanMinute");
    }
  };
};

/** The rules a refusal names (`held`), from the problem document the client keeps. */
const rulesOf = (error: unknown): string[] => (error as { problem?: { rules?: string[] } }).problem?.rules ?? [];

/** A decision's refusal in the reviewer's words; the code is the module's, never the title. */
export const useDecisionError = () => {
  const { t } = useI18n("agents");
  const errorText = useErrorText();
  return (error: unknown): string => {
    switch (errorCode(error)) {
      case "reviewer_not_permitted":
        return t("errorNotPermitted");
      case "item_decided":
        return t("errorDecided");
      case "held": {
        const rules = rulesOf(error).map((code) => t(heldKey(code)));
        return rules.length > 0 ? t("errorHeld", { rules: rules.join("; ") }) : t("errorHeldNone");
      }
      case "channel_refused":
        return t("errorChannelRefused");
      case "change_refused":
        return t("errorChangeRefused");
      case "send_unknown":
        return t("errorSendUnknown");
      default:
        return errorText(error);
    }
  };
};
