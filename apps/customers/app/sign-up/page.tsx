import { Group, Paper, Title } from "@mantine/core";
import { LanguageToggle } from "@vantigo/customers/lib/i18n/language-toggle";
import { getTranslations } from "next-intl/server";
import classes from "../sign-in/sign-in-page.module.css";
import { SignUpForm } from "./sign-up-form";

export default async function SignUpPage() {
  const t = await getTranslations("signUp");

  return (
    <div className={classes.wrapper}>
      <Paper className={classes.form}>
        <Group justify="flex-end">
          <LanguageToggle />
        </Group>

        <Title order={2} className={classes.title}>
          {t("title")}
        </Title>

        <SignUpForm />
      </Paper>
    </div>
  );
}
