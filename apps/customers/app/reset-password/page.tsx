import { Group, Paper, Title } from "@mantine/core";
import { LanguageToggle } from "@vantigo/customers/lib/i18n/language-toggle";
import { getTranslations } from "next-intl/server";
import { Suspense } from "react";
import classes from "../sign-in/sign-in-page.module.css";
import { ResetPasswordForm } from "./reset-password-form";

export default async function ResetPasswordPage() {
  const t = await getTranslations("resetPassword");

  return (
    <div className={classes.wrapper}>
      <Paper className={classes.form}>
        <Group justify="flex-end">
          <LanguageToggle />
        </Group>

        <Title order={2} className={classes.title}>
          {t("title")}
        </Title>

        <Suspense>
          <ResetPasswordForm />
        </Suspense>
      </Paper>
    </div>
  );
}
