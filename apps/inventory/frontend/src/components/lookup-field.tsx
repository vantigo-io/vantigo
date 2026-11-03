import { Loader, TextInput } from "@mantine/core";
import { IconScan } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { lookupQueryOptions } from "../api/lookup";
import { refusalMessage, useInventoryFormat } from "../lib/format";
import { LOOKUP_ROUTE_PATH, matchLinkOptions } from "../lib/routes";

/** The lookup field (I28), for the home page and the host's spotlight. */
export interface InventoryLookupFieldProps {
  /** Called once the field has navigated, so a spotlight can close. */
  onNavigate?: () => void;
  /** Focus the field when it mounts, as the home page does for a scanner; on by default. */
  autoFocus?: boolean;
  /** The text the field opens with: the lookup page keeps what found nothing. */
  initialValue?: string;
}

/**
 * One field that submits on Enter, which is what a keyboard-wedge scanner
 * sends (I28). One match opens it, several open the lookup page listing them,
 * and none says so here and keeps the text, so a mistyped code can be fixed.
 */
export const InventoryLookupField = ({
  onNavigate,
  autoFocus = true,
  initialValue = "",
}: InventoryLookupFieldProps) => {
  const { t } = useInventoryFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const [value, setValue] = useState(initialValue);
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    const q = value.trim();
    if (!q) return;
    setBusy(true);
    setError(undefined);
    try {
      const matches = await queryClient.fetchQuery(lookupQueryOptions(q));
      if (matches.length === 0) {
        setError(t("lookupNothing", { q }));
        return;
      }
      await navigate(matches.length === 1 ? matchLinkOptions(matches[0]) : { to: LOOKUP_ROUTE_PATH, search: { q } });
      onNavigate?.();
    } catch (failure) {
      setError(refusalMessage(t, failure as Error));
    } finally {
      setBusy(false);
    }
  };

  return (
    <TextInput
      data-assist="inventory.home.lookup"
      label={t("lookupLabel")}
      placeholder={t("lookupPlaceholder")}
      description={t("lookupHint")}
      leftSection={<IconScan size={16} />}
      // A scanner types into whatever has focus; the field is the page's first stop.
      autoFocus={autoFocus}
      value={value}
      error={error}
      // Read-only rather than disabled while it asks, so the field keeps its focus for the next scan.
      readOnly={busy}
      rightSection={busy ? <Loader size="xs" /> : undefined}
      onChange={(event) => {
        setValue(event.currentTarget.value);
        setError(undefined);
      }}
      onKeyDown={(event) => {
        if (event.key !== "Enter") return;
        event.preventDefault();
        void submit();
      }}
    />
  );
};
