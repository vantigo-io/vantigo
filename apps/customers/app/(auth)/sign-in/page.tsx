import { getTranslations } from "next-intl/server";
import { AuthTitle } from "../auth-title";
import { SignInForm } from "./sign-in-form";

export default async function SignInPage() {
  const t = await getTranslations("signIn");

  return (
    <>
      <AuthTitle>{t("title")}</AuthTitle>
      <SignInForm />
    </>
  );
}
