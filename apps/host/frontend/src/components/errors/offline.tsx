import { Button } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { ErrorPage } from "./error-page";
import { OfflineIllustration } from "./illustrations";

export const OfflinePage = ({ retry }: { retry?: () => void }) => {
  const { t } = useI18n("host");
  return (
    <ErrorPage
      illustration={<OfflineIllustration />}
      title={t("offlineTitle")}
      message={t("offlineBody")}
      actions={<Button onClick={retry ?? (() => window.location.reload())}>{t("errorRetry")}</Button>}
    />
  );
};
