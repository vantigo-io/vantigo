import { Alert } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { DevErrorDetails } from "./dev-error-details";
import { ErrorPage } from "./error-page";
import { ForbiddenIllustration } from "./illustrations";

type Props = { reason?: string; requiredModule?: string; requiredPermissions?: string[] };
export const ForbiddenPage = ({ reason, requiredModule, requiredPermissions }: Props) => {
  const { t } = useI18n("host");
  return (
    <ErrorPage
      illustration={<ForbiddenIllustration />}
      title={t("forbiddenTitle")}
      message={reason ?? t("forbiddenBody")}
      extra={
        <>
          <Alert color="gray" variant="light">
            {t("contactAdministrator")}
          </Alert>
          {import.meta.env.DEV && <DevErrorDetails context={{ reason, requiredModule, requiredPermissions }} />}
        </>
      }
    />
  );
};
