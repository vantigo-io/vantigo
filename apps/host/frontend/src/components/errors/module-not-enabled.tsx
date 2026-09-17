import { useI18n } from "@vantigo/frontend-shell";
import { ErrorPage } from "./error-page";
import { ForbiddenIllustration } from "./illustrations";

/**
 * Shown in place of an app whose module the installation turned off
 * (MODULES). Enablement is an installation property, so unlike the forbidden
 * page there is nothing the user can be granted; like every other error page
 * it offers the shared "go back" and "go home" actions.
 */
export const ModuleNotEnabledPage = ({ appLabel }: { appLabel: string }) => {
  const { t } = useI18n("host");
  return (
    <ErrorPage
      illustration={<ForbiddenIllustration />}
      title={t("moduleNotEnabledTitle", { app: appLabel })}
      message={t("moduleNotEnabledBody")}
    />
  );
};
