import { Button } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { ErrorPage } from "./error-page";
import { SessionIllustration } from "./illustrations";

export const SessionExpiredPage = () => {
  const { t } = useI18n("host");
  return (
    <ErrorPage
      illustration={<SessionIllustration />}
      title={t("sessionExpiredTitle")}
      message={t("sessionExpiredBody")}
      actions={
        <Button component="a" href="/sign-in">
          {t("sessionSignInAgain")}
        </Button>
      }
    />
  );
};
