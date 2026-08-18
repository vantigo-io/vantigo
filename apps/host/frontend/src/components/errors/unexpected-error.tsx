import { Button } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { DevErrorDetails } from "./dev-error-details";
import { ErrorPage } from "./error-page";
import { CrashIllustration } from "./illustrations";

type Props = { error?: unknown; reset?: () => void };
export const UnexpectedErrorPage = ({ error, reset }: Props) => {
  const { t } = useI18n("host");
  return (
    <ErrorPage
      illustration={<CrashIllustration />}
      title={t("unexpectedErrorTitle")}
      message={t("unexpectedErrorBody")}
      error={error}
      actions={
        <>
          <Button variant="default" onClick={reset}>
            {t("errorRetry")}
          </Button>
          <Button component="a" href="/">
            {t("errorHome")}
          </Button>
        </>
      }
      extra={import.meta.env.DEV ? <DevErrorDetails error={error} /> : undefined}
    />
  );
};
