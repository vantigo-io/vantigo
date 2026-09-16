import { Button } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { ErrorPage } from "./error-page";
import { ForbiddenIllustration } from "./illustrations";

/**
 * Shown in place of an app whose module the installation turned off
 * (MODULES). Enablement is an installation property, so unlike the forbidden
 * page there is nothing the user can be granted; the only way out is Home.
 */
export const ModuleNotEnabledPage = ({ appLabel }: { appLabel: string }) => {
  const { t } = useI18n("host");
  return (
    <ErrorPage
      illustration={<ForbiddenIllustration />}
      title={t("moduleNotEnabledTitle", { app: appLabel })}
      message={t("moduleNotEnabledBody")}
      actions={
        <Button component={Link} to="/dashboard">
          {t("errorDashboard")}
        </Button>
      }
    />
  );
};
