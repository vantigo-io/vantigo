import {
  Alert,
  Anchor,
  Box,
  Button,
  Card,
  Checkbox,
  Container,
  Divider,
  Group,
  MantineProvider,
  Paper,
  PasswordInput,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import {
  createRootRoute,
  createRoute,
  createRouter,
  Navigate,
  Outlet,
  useSearch,
} from "@tanstack/react-router";
import { AuthenticatedShell, commonTheme } from "@vantigo/ui";
import type React from "react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { authClient } from "./auth";
import i18n from "./i18n";

// Root Route: Setup global Providers (Mantine, routing outlets)
const rootRoute = createRootRoute({
  component: () => (
    <MantineProvider theme={commonTheme}>
      <Outlet />
    </MantineProvider>
  ),
});

// Root Index Redirect Route (/) -> (/idp)
const rootIndexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: () => <Navigate to="/idp" />,
});

// App Index Redirect Route (/idp) -> (/idp/account) or (/idp/login)
const idpIndexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/idp",
  component: () => {
    const { data: session, isPending } = authClient.useSession();
    if (isPending) {
      return (
        <Container size="xs" mt="xl" style={{ textAlign: "center" }}>
          <Text size="sm" color="dimmed">
            Loading session...
          </Text>
        </Container>
      );
    }
    if (session?.user) {
      return <Navigate to="/idp/account" />;
    }
    return <Navigate to="/idp/login" search={{ redirectUrl: undefined }} />;
  },
});

interface LoginSearch {
  redirectUrl?: string;
}

// Login and Registration Route Component (Unauthenticated Layout)
function LoginComponent() {
  const { t } = useTranslation();
  const search = useSearch({ from: loginRoute.id }) as LoginSearch;
  const redirectUrl = search.redirectUrl;

  const [type, setType] = useState<"login" | "register">("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");

    // Simple validation before calling Better-Auth client
    if (!email) {
      setError(t("errorEmailRequired"));
      return;
    }
    if (!password) {
      setError(t("errorPasswordRequired"));
      return;
    }
    if (type === "register" && !name) {
      setError(t("errorNameRequired"));
      return;
    }

    setLoading(true);
    try {
      if (type === "login") {
        const { error: signInError } = await authClient.signIn.email({
          email,
          password,
        });
        if (signInError) {
          setError(signInError.message || "Invalid credentials");
        } else {
          window.location.href = redirectUrl || "/idp/account";
        }
      } else {
        const { error: signUpError } = await authClient.signUp.email({
          email,
          password,
          name,
        });
        if (signUpError) {
          setError(signUpError.message || "Failed to register account");
        } else {
          window.location.href = "/idp/account";
        }
      }
    } catch (err) {
      const errorObject = err as Error;
      setError(errorObject.message || "An unexpected error occurred");
    } finally {
      setLoading(false);
    }
  };

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
          {type === "login" ? t("subtitleLogin") : t("subtitleRegister")}
          <Anchor
            size="sm"
            component="button"
            onClick={() => {
              setError("");
              setType(type === "login" ? "register" : "login");
            }}
          >
            {type === "login" ? t("registerLink") : t("loginLink")}
          </Anchor>
        </Text>

        {error && (
          <Alert color="red" title="Error" mb="md" variant="light">
            {error}
          </Alert>
        )}

        <form onSubmit={handleSubmit}>
          <Stack>
            {type === "register" && (
              <TextInput
                label={t("name")}
                placeholder={t("placeholderName")}
                value={name}
                onChange={(event) => setName(event.currentTarget.value)}
                required
              />
            )}

            <TextInput
              label={t("email")}
              placeholder={t("placeholderEmail")}
              value={email}
              onChange={(event) => setEmail(event.currentTarget.value)}
              required
            />

            <PasswordInput
              label={t("password")}
              placeholder={t("placeholderPassword")}
              value={password}
              onChange={(event) => setPassword(event.currentTarget.value)}
              required
            />

            {type === "login" && (
              <Group justify="space-between" mt="xs">
                <Checkbox label={t("rememberMe")} />
                <Anchor component="button" size="sm" type="button">
                  {t("forgotPassword")}
                </Anchor>
              </Group>
            )}

            <Button type="submit" fullWidth mt="xl" loading={loading}>
              {type === "login" ? t("signIn") : t("signUp")}
            </Button>
          </Stack>
        </form>
      </Paper>
    </Container>
  );
}

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/idp/login",
  validateSearch: (search: Record<string, unknown>) => {
    return {
      redirectUrl: (search.redirectUrl as string) || undefined,
    };
  },
  component: LoginComponent,
});

// Protected Profile Account Route Component (Authenticated Layout)
function AccountComponent() {
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

          <Card withBorder shadow="sm" radius="md" p="xl">
            <Text fw={700} size="lg" mb="md">
              {t("profileInfo")}
            </Text>
            <Divider mb="lg" />
            <Stack gap="md">
              <Group justify="space-between">
                <Text fw={500}>{t("name")}</Text>
                <Text color="dimmed">{session.user.name}</Text>
              </Group>
              <Group justify="space-between">
                <Text fw={500}>{t("email")}</Text>
                <Text color="dimmed">{session.user.email}</Text>
              </Group>
            </Stack>
          </Card>

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

const accountRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/idp/account",
  component: AccountComponent,
});

// Configure Route Tree
const routeTree = rootRoute.addChildren([
  rootIndexRoute,
  idpIndexRoute,
  loginRoute,
  accountRoute,
]);

export const router = createRouter({
  routeTree,
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
