import { auth } from "@vantigo/customers/lib/auth";
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

  return (
    <SettingsPage
      user={{
        name: session.user.name,
        email: session.user.email,
        image: session.user.image ?? null,
        preferredCustomerType: session.user.preferredCustomerType ?? null,
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
