import { getTranslations } from "next-intl/server";
import { AuthTitle } from "../auth-title";
import { SignUpForm } from "./sign-up-form";

export default async function SignUpPage() {
  const t = await getTranslations("signUp");

  return (
    <>
      <AuthTitle>{t("title")}</AuthTitle>
      <SignUpForm />
    </>
  );
}
