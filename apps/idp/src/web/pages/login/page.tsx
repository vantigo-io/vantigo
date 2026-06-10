import {
  Anchor,
  Button,
  Container,
  Group,
  Paper,
  Text,
  Title,
} from "@mantine/core";
import { useSearch } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "../../i18n";
import { LoginForm } from "./LoginForm";

export function LoginPage() {
  const { t } = useTranslation();
  const search = useSearch({ strict: false }) as { redirectUrl?: string };
  const redirectUrl = search.redirectUrl;

  const [type, setType] = useState<"login" | "register">("login");

  const toggleLanguage = () => {
    const nextLng = i18n.language === "en" ? "no" : "en";
    i18n.changeLanguage(nextLng);
  };

  return (
    <Container size={420} my={80}>
      {/* Dynamic Language Toggle on top of Login Card */}
      <Group justify="flex-end" mb="md">
        <Button variant="subtle" size="xs" onClick={toggleLanguage}>
          {i18n.language === "en" ? "🇳🇴 Norsk" : "🇬🇧 English"}
        </Button>
      </Group>

      <Paper withBorder shadow="md" p={30} mt={30} radius="md">
        <Title
          ta="center"
          style={{
            fontFamily: "Greycliff CF, var(--mantine-font-family)",
            fontWeight: 900,
            fontSize: "1.6rem",
          }}
          mb="xs"
        >
          {type === "login" ? t("welcome") : t("welcomeSignup")}
        </Title>
        <Text color="dimmed" size="sm" ta="center" mb="lg">
          {type === "login" ? t("subtitleLogin") : t("subtitleRegister")}{" "}
          <Anchor
            size="sm"
            component="button"
            onClick={() => {
              setType(type === "login" ? "register" : "login");
            }}
          >
            {type === "login" ? t("registerLink") : t("loginLink")}
          </Anchor>
        </Text>

        <LoginForm redirectUrl={redirectUrl} type={type} />
      </Paper>
    </Container>
  );
}
