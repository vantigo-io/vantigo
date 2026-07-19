import { auth } from "@vantigo/customers/lib/auth";
import { resolveDefaultLegalType } from "@vantigo/customers/lib/settings/preferences";
import { headers } from "next/headers";
import { CustomersPage } from "./customers-page";

export default async function Page() {
  const session = await auth.api.getSession({ headers: await headers() });

  return (
    <CustomersPage
      defaultLegalType={resolveDefaultLegalType(
        session?.user.preferredCustomerType,
        session?.user.lastCustomerType,
      )}
    />
  );
}
