"use client";

import { Anchor, Stack, Text } from "@mantine/core";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { TwoFactorEnrollment } from "@vantigo/customers/lib/components/two-factor-enrollment";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";

export function TwoFactorSetup() {
  const t = useTranslations("twoFactor.setup");
  const router = useRouter();

  const handleSignOut = async () => {
    await authClient.signOut();
    router.push("/sign-in");
    router.refresh();
  };

  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed" ta="center">
        {t("description")}
      </Text>

      <TwoFactorEnrollment
        onEnabled={() => {
          router.push("/");
          router.refresh();
        }}
      />

      <Text ta="center" size="sm">
        <Anchor component="button" type="button" onClick={handleSignOut}>
          {t("signOut")}
        </Anchor>
      </Text>
    </Stack>
  );
}
