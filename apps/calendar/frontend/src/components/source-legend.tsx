import { Box, Chip, Group } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { JSX } from "react";
import "../i18n";
import { colourForKind, kindLabelKey } from "../lib/colours";

export interface SourceLegendProps {
  /** The kinds to offer, in order: appointments, the providers' from `/meta`, the holidays. */
  kinds: readonly string[];
  hidden: ReadonlySet<string>;
  onToggle: (kind: string) => void;
}

/**
 * The legend and the filter in one (design D8): a chip per kind in its
 * module's colour, checked while the kind is shown. Unchecking hides it, and
 * the choice is remembered in this browser.
 */
export function SourceLegend({ kinds, hidden, onToggle }: SourceLegendProps): JSX.Element {
  const { t, i18n } = useI18n("calendar");
  // A kind from a module newer than this catalog reads as itself rather than failing.
  const label = (kind: string) => (i18n.exists(kindLabelKey(kind), { ns: "calendar" }) ? t(kindLabelKey(kind)) : kind);
  return (
    <Group gap={6} role="group" aria-label={t("legend")}>
      {kinds.map((kind) => (
        <Chip key={kind} size="xs" variant="outline" checked={!hidden.has(kind)} onChange={() => onToggle(kind)}>
          <Group gap={6} wrap="nowrap" component="span">
            <Box component="span" w={10} h={10} style={{ borderRadius: 2, background: colourForKind(kind) }} />
            {label(kind)}
          </Group>
        </Chip>
      ))}
    </Group>
  );
}
