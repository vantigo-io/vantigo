import { Paper, Title } from "@mantine/core";
import { auth } from "@vantigo/customers/lib/auth";
import { config } from "@vantigo/customers/lib/config";
import { isTwoFactorAvailable } from "@vantigo/customers/lib/settings/two-factor";
import { headers } from "next/headers";
import { redirect } from "next/navigation";
import { getTranslations } from "next-intl/server";
import classes from "../../sign-in/sign-in-page.module.css";
import { TwoFactorSetup } from "./two-factor-setup";

export default async function TwoFactorSetupPage() {
  const twoFactorConfig = {
    enabled: config.VANTIGO_CUSTOMERS_2FA_ENABLED,
    enforced: config.VANTIGO_CUSTOMERS_2FA_ENFORCED,
  };

  if (!isTwoFactorAvailable(twoFactorConfig)) {
    redirect("/");
  }

  const session = await auth.api.getSession({ headers: await headers() });
  if (!session?.user) {
    redirect("/sign-in");
  }
  if (session.user.twoFactorEnabled) {
    redirect("/");
  }

  const t = await getTranslations("twoFactor.setup");

  return (
    <div className={classes.wrapper}>
      <Paper className={classes.form}>
        <Title order={2} className={classes.title}>
          {t("title")}
        </Title>

        <TwoFactorSetup />
      </Paper>
    </div>
  );
}
