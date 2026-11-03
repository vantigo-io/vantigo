import { useI18n } from "@vantigo/frontend-shell";
import type { StaffAccountKind } from "../api/accounts";
import type { StaffMeta } from "../api/meta";
import { staffCatalog } from "../i18n";

type Translate = (key: string, values?: Record<string, unknown>) => string;
type Provider = NonNullable<StaffMeta["providers"]>[number];

/** identity's pages an Owner acts on (delivery C): the users — adoption, a disable — and the roles Staff may give. */
export const WORKSPACE_USERS_PATH = "/workspace/users";
export const WORKSPACE_ROLES_PATH = "/workspace/roles";

/** The kinds that need no provider; any other is a provider's key, offered only while an enabled module offers it. */
const builtInKinds: StaffAccountKind[] = ["local", "external"];

/** Whether the kind names a provider's directory (`entra`, say), whose link the Account tab shows beside Vantigo's. */
export const isProviderKind = (kind: StaffAccountKind) => !builtInKinds.includes(kind);

/**
 * The kinds HR may choose (Google design G6): `local` and `external` always,
 * and each provider's key in `/meta`'s providers — and the kind already
 * chosen, so a provider that went away is still said.
 */
export const accountKindsOffered = (
  providers: StaffMeta["providers"],
  current?: StaffAccountKind,
): StaffAccountKind[] => {
  const offered = [...builtInKinds];
  for (const provider of providers ?? []) if (!offered.includes(provider.key)) offered.push(provider.key);
  if (current && !offered.includes(current)) offered.push(current);
  return offered;
};

/**
 * A kind's short name, for a choice or a card's title: a provider's name
 * from `/meta`'s providers (staff:manage's alone), else the catalog's —
 * `local`, `external`, and a provider's name for a reader without
 * `providers` — else the key itself, for a provider no enabled module offers.
 */
export const accountKindName = (t: Translate, kind: StaffAccountKind, providers: StaffMeta["providers"]): string =>
  providers?.find((provider) => provider.key === kind)?.name ??
  (`accountKindChoice_${kind}` in staffCatalog.en ? t(`accountKindChoice_${kind}`) : kind);

/** The account kind in words (the person's or the default); absent, accounts are unavailable. */
export const useAccountKindText = (providers: StaffMeta["providers"]) => {
  const { t } = useI18n("staff");
  return (kind: StaffAccountKind | undefined) =>
    kind && isProviderKind(kind)
      ? t("accountKind_provider", { name: accountKindName(t, kind, providers) })
      : t(`accountKind_${kind ?? "unavailable"}`);
};

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
