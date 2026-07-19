import { auth } from "@vantigo/customers/lib/auth";
import { config } from "@vantigo/customers/lib/config";
import { isTwoFactorAvailable } from "@vantigo/customers/lib/settings/two-factor";
import { headers } from "next/headers";
import { redirect } from "next/navigation";
import { SettingsPage } from "./settings-page";

export default async function Page() {
  const requestHeaders = await headers();
  const session = await auth.api.getSession({ headers: requestHeaders });

  if (!session?.user) {
    redirect("/sign-in");
  }

  const sessions = await auth.api.listSessions({ headers: requestHeaders });

  const twoFactorConfig = {
    enabled: config.VANTIGO_CUSTOMERS_2FA_ENABLED,
    enforced: config.VANTIGO_CUSTOMERS_2FA_ENFORCED,
  };

  return (
    <SettingsPage
      user={{
        name: session.user.name,
        email: session.user.email,
        image: session.user.image ?? null,
        preferredCustomerType: session.user.preferredCustomerType ?? null,
      }}
      twoFactor={{
        available: isTwoFactorAvailable(twoFactorConfig),
        enforced: twoFactorConfig.enforced,
        enabled: session.user.twoFactorEnabled ?? false,
      }}
      sessions={sessions
        .map((s) => ({
          id: s.id,
          token: s.token,
          createdAt: s.createdAt.toISOString(),
          expiresAt: s.expiresAt.toISOString(),
          ipAddress: s.ipAddress ?? null,
          userAgent: s.userAgent ?? null,
          current: s.id === session.session.id,
        }))
        .sort((a, b) => (a.current ? -1 : b.current ? 1 : b.createdAt.localeCompare(a.createdAt)))}
    />
  );
}
