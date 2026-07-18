import {
  Anchor,
  Button,
  Checkbox,
  Group,
  Paper,
  PasswordInput,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { LanguageToggle } from "@vantigo/customers/lib/i18n/language-toggle";
import { getTranslations } from "next-intl/server";
import classes from "./sign-in-page.module.css";

export default async function SignInPage() {
  const t = await getTranslations("signIn");

  return (
    <div className={classes.wrapper}>
      <Paper className={classes.form}>
        <Group justify="flex-end">
          <LanguageToggle />
        </Group>

        <Title order={2} className={classes.title}>
          {t("title")}
        </Title>

        <TextInput
          label={t("emailLabel")}
          placeholder={t("emailPlaceholder")}
          size="md"
          radius="md"
        />
        <PasswordInput
          label={t("passwordLabel")}
          placeholder={t("passwordPlaceholder")}
          mt="md"
          size="md"
          radius="md"
        />
        <Checkbox label={t("keepLoggedIn")} mt="xl" size="md" />
        <Button fullWidth mt="xl" size="md" radius="md">
          {t("loginButton")}
        </Button>

        <Text ta="center" mt="md">
          {t("noAccount")}{" "}
          <Anchor href="#" fw={500}>
            {t("register")}
          </Anchor>
        </Text>
      </Paper>
    </div>
  );
}
