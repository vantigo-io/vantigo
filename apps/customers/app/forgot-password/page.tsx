import { Group, Paper, Title } from "@mantine/core";
import { LanguageToggle } from "@vantigo/customers/lib/i18n/language-toggle";
import { getTranslations } from "next-intl/server";
import classes from "../sign-in/sign-in-page.module.css";
import { ForgotPasswordForm } from "./forgot-password-form";

export default async function ForgotPasswordPage() {
  const t = await getTranslations("forgotPassword");

  return (
    <div className={classes.wrapper}>
      <Paper className={classes.form}>
        <Group justify="flex-end">
          <LanguageToggle />
        </Group>

        <Title order={2} className={classes.title}>
          {t("title")}
        </Title>

        <ForgotPasswordForm />
      </Paper>
    </div>
  );
}
