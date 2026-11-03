import { Text } from "@mantine/core";
import { OutlookConnectButton, OutlookConnectionCard } from "@vantigo/entra-ui";
import { useTranslation } from "@vantigo/frontend-shell";
import { type ComponentType, type ReactNode, useCallback, useState } from "react";
import type { ModuleKey } from "../navigation";
import { enabledModuleKeys } from "./enabled-modules";

/** What a provider's connect returned with: `complete`, or `failed` with a short code. */
export interface ConnectionOutcome {
  outcome: "complete" | "failed";
  code?: string;
}

/** What the Connections page hands each provider's card. */
export interface ConnectionCardProps {
  /** The redirect's outcome, when the page was opened by this provider's callback. */
  outcome?: ConnectionOutcome;
  /** Where to go once a connect completed: always a path on this installation. */
  onReturn: (to: string) => void;
  /**
   * The card tells whether its provider offers the person anything, once it
   * knows: a card that offers nothing renders nothing, and the page says
   * "nothing to connect" itself when every card has said so.
   */
  onOffered: (offered: boolean) => void;
}

/**
 * What the `connection.button` slot of the calendar settings and the inbox
 * hands each provider's button: the feature asking, and the path to come back
 * to after the redirect.
 */
export interface ConnectionButtonProps {
  feature: "calendar" | "mail";
  returnTo: string;
}

/**
 * A module that connects a person's own account elsewhere (Google Workspace
 * design G9): its card on Settings › Connections and its button in other
 * modules' `connection.button` slot. Each renders nothing when its provider
 * offers the person nothing, so the host lists every enabled one.
 */
export interface ConnectionProvider {
  module: ModuleKey;
  Card: ComponentType<ConnectionCardProps>;
  Button: ComponentType<ConnectionButtonProps>;
}

/** Entra's card speaks its callback's `outlook=` words; the host hands it the neutral outcome in them. */
const EntraConnectionCard = ({ outcome, onReturn, onOffered }: ConnectionCardProps) => (
  <OutlookConnectionCard
    outcome={outcome ? { outlook: outcome.outcome, ...(outcome.code ? { code: outcome.code } : {}) } : undefined}
    onReturn={onReturn}
    onOffered={onOffered}
  />
);

/** Every provider this build knows, in the order the page lists them. Google's joins in its delivery D. */
export const connectionProviders: readonly ConnectionProvider[] = [
  { module: "entra", Card: EntraConnectionCard, Button: OutlookConnectButton },
];

/** The providers whose module is on. */
export const enabledConnectionProviders = (
  providers: readonly ConnectionProvider[] = connectionProviders,
  enabled: readonly ModuleKey[] = enabledModuleKeys(),
): readonly ConnectionProvider[] => providers.filter((provider) => enabled.includes(provider.module));

/**
 * The composite `connection.button`: every enabled provider's button, each
 * rendering nothing for a person whose account is not its own. Undefined when
 * no provider's module is on, so the slot shows nothing in its place.
 */
export const connectionButtonFor = (
  providers: readonly ConnectionProvider[] = enabledConnectionProviders(),
): ((props: ConnectionButtonProps) => ReactNode) | undefined =>
  providers.length === 0
    ? undefined
    : (props) => (
        <>
          {providers.map(({ module, Button }) => (
            <Button key={module} {...props} />
          ))}
        </>
      );

export interface ConnectionsSearch {
  /** Entra's callback's own word (Outlook connection design M4), kept until Entra says `connection=entra`. */
  outlook?: "complete" | "failed";
  /** The neutral form (Google Workspace design G9): the module whose connect returned here. */
  connection?: string;
  outcome?: "complete" | "failed";
  code?: string;
}

const word = (value: unknown, max: number) =>
  typeof value === "string" && new RegExp(`^[a-z_]{1,${max}}$`).test(value) ? value : undefined;

const outcomeOf = (value: unknown) => (value === "complete" || value === "failed" ? value : undefined);

/**
 * What a provider's callback redirects here with: Entra's `outlook=complete`
 * or `outlook=failed&code=…`, or the neutral `connection=<module>&outcome=complete`
 * or `…&outcome=failed&code=…`. Anything else is dropped; a module is kept
 * only as a short lower-case word and only with its outcome, and a code only
 * beside a failure, as a short lower-case word: the card looks its sentence up
 * and never shows it as it came.
 */
export const validateConnectionsSearch = (search: Record<string, unknown>): ConnectionsSearch => {
  const outlook = outcomeOf(search.outlook);
  const connection = word(search.connection, 40);
  const outcome = connection ? outcomeOf(search.outcome) : undefined;
  const failed = outlook === "failed" || outcome === "failed";
  const code = failed ? word(search.code, 40) : undefined;
  return {
    ...(outlook ? { outlook } : {}),
    ...(connection && outcome ? { connection, outcome } : {}),
    ...(code ? { code } : {}),
  };
};

/** The outcome that belongs to `module`'s card: its own `connection=`, or Entra's `outlook=`. */
export const outcomeFor = (module: string, search: ConnectionsSearch): ConnectionOutcome | undefined => {
  const outcome =
    search.connection === module
      ? search.outcome
      : module === "entra" && !search.connection
        ? search.outlook
        : undefined;
  if (!outcome) return undefined;
  return { outcome, ...(outcome === "failed" && search.code ? { code: search.code } : {}) };
};

export interface ConnectionCardsProps {
  providers: readonly ConnectionProvider[];
  search: ConnectionsSearch;
  onReturn: (to: string) => void;
}

/**
 * Each provider's card, in the list's order, handed the outcome that is its
 * own. A card that offers the person nothing renders nothing; once every card
 * has said so, the page says there is nothing to connect.
 */
export const ConnectionCards = ({ providers, search, onReturn }: ConnectionCardsProps) => {
  const { t } = useTranslation("host");
  const [offers, setOffers] = useState<Readonly<Record<string, boolean>>>({});
  const tell = useCallback(
    (module: string, offered: boolean) =>
      setOffers((now) => (now[module] === offered ? now : { ...now, [module]: offered })),
    [],
  );
  const nothing = providers.length > 0 && providers.every(({ module }) => offers[module] === false);
  return (
    <>
      {providers.map(({ module, Card }) => (
        <ProviderCard
          key={module}
          module={module}
          Card={Card}
          outcome={outcomeFor(module, search)}
          onReturn={onReturn}
          tell={tell}
        />
      ))}
      {nothing && (
        <Text size="sm" c="dimmed">
          {t("settings.connectionsNothing")}
        </Text>
      )}
    </>
  );
};

interface ProviderCardProps extends Omit<ConnectionCardProps, "onOffered"> {
  module: string;
  Card: ComponentType<ConnectionCardProps>;
  tell: (module: string, offered: boolean) => void;
}

/** One provider's card, its offer told to the list under its module. */
const ProviderCard = ({ module, Card, outcome, onReturn, tell }: ProviderCardProps) => {
  const onOffered = useCallback((offered: boolean) => tell(module, offered), [tell, module]);
  return <Card outcome={outcome} onReturn={onReturn} onOffered={onOffered} />;
};
