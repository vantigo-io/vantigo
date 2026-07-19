import { auth } from "@vantigo/customers/lib/auth";
import { isTwoFactorAvailable } from "@vantigo/customers/lib/settings/two-factor";
import { getTwoFactorConfig } from "@vantigo/customers/lib/settings/two-factor-config";
import { headers } from "next/headers";
import { redirect } from "next/navigation";
import { getTranslations } from "next-intl/server";
import { AuthTitle } from "../../auth-title";
import { TwoFactorSetup } from "./two-factor-setup";

export default async function TwoFactorSetupPage() {
  if (!isTwoFactorAvailable(getTwoFactorConfig())) {
    redirect("/");
  }

  const session = await auth.api.getSession({ headers: await headers() });
  if (!session?.user) {
    redirect("/sign-in");
  }
  if (session.user.twoFactorEnabled) {
    redirect("/");
  }

  const t = await getTranslations("twoFactor.setup");

  return (
    <>
      <AuthTitle>{t("title")}</AuthTitle>
      <TwoFactorSetup />
    </>
  );
}
