import { useI18n } from "@vantigo/frontend-shell";
import { DevErrorDetails } from "./dev-error-details";
import { ErrorPage } from "./error-page";
import { NotFoundIllustration } from "./illustrations";

export const NotFoundPage = () => {
  const { t } = useI18n("host");
  return (
    <ErrorPage
      illustration={<NotFoundIllustration />}
      title={t("notFoundTitle")}
      message={t("notFoundBody")}
      extra={
        import.meta.env.DEV ? <DevErrorDetails context={{ attemptedPathname: window.location.pathname }} /> : undefined
      }
    />
  );
};
