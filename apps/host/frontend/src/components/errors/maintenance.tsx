import { Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { ErrorPage } from "./error-page";
import { MaintenanceIllustration } from "./illustrations";

export const MaintenancePage = ({ message }: { message?: string | null }) => {
  const { t } = useI18n("host");
  return (
    <ErrorPage
      fullscreen
      illustration={<MaintenanceIllustration />}
      title={t("maintenanceTitle")}
      message={
        message ? (
          <Text component="span" style={{ whiteSpace: "pre-wrap" }}>
            {message}
          </Text>
        ) : (
          t("maintenanceBody")
        )
      }
    />
  );
};
