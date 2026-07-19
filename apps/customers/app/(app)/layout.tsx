import { auth } from "@vantigo/customers/lib/auth";
import { config } from "@vantigo/customers/lib/config";
import { requiresTwoFactorSetup } from "@vantigo/customers/lib/settings/two-factor";
import { headers } from "next/headers";
import { redirect } from "next/navigation";
import { Providers } from "./providers";
import { Shell } from "./shell";

export default async function AppLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  const session = await auth.api.getSession({ headers: await headers() });

  if (!session?.user) {
    redirect("/sign-in");
  }

  if (
    requiresTwoFactorSetup(
      {
        enabled: config.VANTIGO_CUSTOMERS_2FA_ENABLED,
        enforced: config.VANTIGO_CUSTOMERS_2FA_ENFORCED,
      },
      session.user.twoFactorEnabled,
    )
  ) {
    redirect("/two-factor/setup");
  }

  return (
    <Providers>
      <Shell
        user={{
          name: session.user.name,
          email: session.user.email,
          image: session.user.image ?? undefined,
        }}
      >
        {children}
      </Shell>
    </Providers>
  );
}
