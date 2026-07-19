import { auth } from "@vantigo/customers/lib/auth";
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
