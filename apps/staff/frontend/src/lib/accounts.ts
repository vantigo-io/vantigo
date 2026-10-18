import { useI18n } from "@vantigo/frontend-shell";
import type { StaffPersonAccount } from "../api/accounts";
import "../i18n";

/** identity's pages an Owner acts on (delivery C): the users — adoption, a disable — and the roles Staff may give. */
export const WORKSPACE_USERS_PATH = "/workspace/users";
export const WORKSPACE_ROLES_PATH = "/workspace/roles";

/** The authority mode in words (`/meta`'s or the account's); absent, accounts are unavailable. */
export const useAccountModeText = () => {
  const { t } = useI18n("staff");
  return (mode: StaffPersonAccount["mode"]) => t(`accountMode_${mode ?? "unavailable"}`);
};
