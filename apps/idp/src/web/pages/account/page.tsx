import {
  Box,
  Button,
  Container,
  Group,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { Navigate } from "@tanstack/react-router";
import { AuthenticatedShell } from "@vantigo/ui";
import { useTranslation } from "react-i18next";
import { authClient } from "../../auth";
import i18n from "../../i18n";
import { ProfileCard } from "./ProfileCard";

export function AccountPage() {
  const { t } = useTranslation();
  const { data: session, isPending } = authClient.useSession();

  if (isPending) {
    return (
      <Container size="xs" mt="xl" style={{ textAlign: "center" }}>
        <Text size="sm" color="dimmed">
          Loading profile...
        </Text>
      </Container>
    );
  }

  if (!session?.user) {
    // If not authenticated, redirect to login page preserving current target
    return (
      <Navigate to="/idp/login" search={{ redirectUrl: "/idp/account" }} />
    );
  }

  const handleSignOut = async () => {
    await authClient.signOut();
    window.location.href = "/idp/login";
  };

  const toggleLanguage = () => {
    const nextLng = i18n.language === "en" ? "no" : "en";
    i18n.changeLanguage(nextLng);
  };

  return (
    <AuthenticatedShell
      user={{
        name: session.user.name,
        email: session.user.email,
        image: session.user.image || undefined,
      }}
      currentAppName={t("appName")}
      onSignOut={handleSignOut}
    >
      <Container size="sm" mt="md">
        <Stack gap="lg">
          <Box>
            <Title order={2}>{t("accountTitle")}</Title>
            <Text color="dimmed" size="sm">
              {t("accountSubtitle")}
            </Text>
          </Box>

          <ProfileCard
            user={{
              name: session.user.name,
              email: session.user.email,
            }}
          />

          <Group justify="center" mt="md">
            <Button variant="subtle" size="xs" onClick={toggleLanguage}>
              {i18n.language === "en"
                ? "Vis på Norsk 🇳🇴"
                : "Show in English 🇬🇧"}
            </Button>
          </Group>
        </Stack>
      </Container>
    </AuthenticatedShell>
  );
}
