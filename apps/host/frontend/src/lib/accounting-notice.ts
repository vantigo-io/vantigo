import { useQuery } from "@tanstack/react-query";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { hasPermissions } from "../navigation";
import { enabledModuleKeys } from "./enabled-modules";

/**
 * Whether Expenses' form shows Accounting's pointer on its supplier invoice
 * kind (accounting design A20, R2): Accounting on in MODULES and the caller
 * able to read the books, whose meta then says whether they are set up. The
 * queries share the root layout's keys, as useInvoiceAccess's do.
 */
export const useAccountingNotice = (): boolean => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return (
    enabledModuleKeys().includes("accounting") && hasPermissions(authorization.data?.permissions, ["accounting:read"])
  );
};
