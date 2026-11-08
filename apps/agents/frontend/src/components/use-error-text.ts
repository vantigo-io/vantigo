import { useI18n } from "@vantigo/frontend-shell";
import { isOwnerRequired } from "../api/request";

/** The words for a refused request: the Owner-only refusal in the viewer's language, else the server's own words. */
export const useErrorText = () => {
  const { t } = useI18n("agents");
  return (error: unknown): string =>
    isOwnerRequired(error)
      ? t("ownerRequired")
      : error instanceof Error && error.message
        ? error.message
        : t("requestFailed");
};
