import { useI18n } from "@vantigo/frontend-shell";
import type { StaffAccountKind } from "../api/accounts";
import type { StaffMeta } from "../api/meta";
import { staffCatalog } from "../i18n";

/** identity's pages an Owner acts on (delivery C): the users — adoption, a disable — and the roles Staff may give. */
export const WORKSPACE_USERS_PATH = "/workspace/users";
export const WORKSPACE_ROLES_PATH = "/workspace/roles";

/** The kinds that need no provider; any other is a provider's key, offered only while an enabled module offers it. */
const builtInKinds: StaffAccountKind[] = ["local", "external"];

/** Whether the kind names a provider's directory (`entra`), whose link the Account tab shows beside Vantigo's. */
export const isProviderKind = (kind: StaffAccountKind) => !builtInKinds.includes(kind);

/** The provider's key that the `entra` kind names (Entra design D5). */
export const ENTRA_KIND: StaffAccountKind = "entra";

/**
 * The kinds HR may choose (Entra design D5): `local` and `external` always,
 * `entra` only while `/meta`'s providers hold a provider with that key — and
 * the kind already chosen, so a provider that went away is still said.
 */
export const accountKindsOffered = (
  providers: StaffMeta["providers"],
  current?: StaffAccountKind,
): StaffAccountKind[] => {
  const offered = [...builtInKinds];
  if (providers?.some((provider) => provider.key === ENTRA_KIND)) offered.push(ENTRA_KIND);
  if (current && !offered.includes(current)) offered.push(current);
  return offered;
};

/** The account kind in words (the person's or the default); absent, accounts are unavailable. */
export const useAccountKindText = () => {
  const { t } = useI18n("staff");
  return (kind: StaffAccountKind | undefined) => t(`accountKind_${kind ?? "unavailable"}`);
};

type Translate = (key: string, values?: Record<string, unknown>) => string;
type Provider = NonNullable<StaffMeta["providers"]>[number];

/**
 * A provider's status line (Settings, Accounts): its name and status in
 * words, with the reason's code said when the catalog has it — a code a
 * later server writes is said by its code.
 */
export const providerStatusText = (t: Translate, provider: Provider): string => {
  const status = t(`providerStatus_${provider.status}`, { name: provider.name });
  if (!provider.reason) return status;
  const key = `providerStatusReason_${provider.reason}`;
  const reason = key in staffCatalog.en ? t(key) : provider.reason;
  return `${status} ${reason}`;
};
