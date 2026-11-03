import { Text } from "@mantine/core";
import { useTranslation } from "@vantigo/frontend-shell";
import { type ComponentType, useCallback, useState } from "react";
import {
  type ConnectionCardProps,
  type ConnectionProvider,
  type ConnectionsSearch,
  outcomeFor,
} from "../lib/connections";
import "../i18n";

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
