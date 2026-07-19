import { Paper, Title } from "@mantine/core";
import { getTranslations } from "next-intl/server";
import classes from "../sign-in/sign-in-page.module.css";
import { TwoFactorVerifyForm } from "./two-factor-verify-form";

export default async function TwoFactorPage() {
  const t = await getTranslations("twoFactor.verify");

  return (
    <div className={classes.wrapper}>
      <Paper className={classes.form}>
        <Title order={2} className={classes.title}>
          {t("title")}
        </Title>

        <TwoFactorVerifyForm />
      </Paper>
    </div>
  );
}
