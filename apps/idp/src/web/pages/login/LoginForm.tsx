import {
  Alert,
  Anchor,
  Button,
  Checkbox,
  Group,
  PasswordInput,
  Stack,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import type React from "react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { authClient } from "../../auth";
import { getVantigoConfig } from "../../config";

interface LoginFormProps {
  redirectUrl?: string;
  type: "login" | "register";
}

export function LoginForm({ redirectUrl, type }: LoginFormProps) {
  const config = getVantigoConfig();
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");

    if (!email) {
      const msg = t("errorEmailRequired");
      setError(msg);
      notifications.show({
        title: t("errorTitle"),
        message: msg,
        color: "red",
      });
      return;
    }
    if (!password) {
      const msg = t("errorPasswordRequired");
      setError(msg);
      notifications.show({
        title: t("errorTitle"),
        message: msg,
        color: "red",
      });
      return;
    }
    if (type === "register" && !name) {
      const msg = t("errorNameRequired");
      setError(msg);
      notifications.show({
        title: t("errorTitle"),
        message: msg,
        color: "red",
      });
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
          const errMsg = signInError.message || t("errorInvalidCredentials");
          setError(errMsg);
          notifications.show({
            title: t("errorTitle"),
            message: errMsg,
            color: "red",
          });
        } else {
          notifications.show({
            title: t("successTitle"),
            message: t("successSignIn"),
            color: "green",
          });
          window.location.href = redirectUrl || `${config.sitePath}/account`;
        }
      } else {
        const { error: signUpError } = await authClient.signUp.email({
          email,
          password,
          name,
        });
        if (signUpError) {
          const errMsg = signUpError.message || t("errorRegisterFailed");
          setError(errMsg);
          notifications.show({
            title: t("errorTitle"),
            message: errMsg,
            color: "red",
          });
        } else {
          notifications.show({
            title: t("successTitle"),
            message: t("successSignUp"),
            color: "green",
          });
          window.location.href = `${config.sitePath}/account`;
        }
      }
    } catch (err) {
      const errorObject = err as Error;
      const errMsg = errorObject.message || t("errorUnexpected");
      setError(errMsg);
      notifications.show({
        title: t("errorTitle"),
        message: errMsg,
        color: "red",
      });
    } finally {
      setLoading(false);
    }
  };

  return (
    <>
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
    </>
  );
}
