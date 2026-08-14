import type { Icon } from "@tabler/icons-react";
import { IconPencil } from "@tabler/icons-react";

import brregLogo from "../assets/sources/brreg.svg";

/**
 * Describes a provider of legal identity data. Sources with a logo are displayed
 * with their brand mark; sources without one fall back to a generic icon.
 */
export interface LegalSourceInfo {
  /** Human-readable name of the source. */
  label: string;
  /** Translation key for the source label, when the catalog provides one. */
  labelKey?: "legalSourcesBrreg" | "legalSourcesManual";
  /** Brand logo displayed alongside the data, when the source has one. */
  logo?: string;
  /** Link to the source's website. */
  url?: string;
  /** Builds a deep link to the source's page for a specific entity. */
  entityUrl?: (legalId: string) => string;
  /** Fallback icon for sources without a logo. */
  icon?: Icon;
}

const legalSources: Record<string, LegalSourceInfo> = {
  brreg: {
    label: "Brønnøysundregistrene",
    labelKey: "legalSourcesBrreg",
    logo: brregLogo,
    url: "https://www.brreg.no",
    entityUrl: (legalId) => `https://virksomhet.brreg.no/nb/oppslag/enheter/${legalId}`,
  },
  manual: {
    label: "Manual entry",
    labelKey: "legalSourcesManual",
    icon: IconPencil,
  },
};

/** Resolves a legal source code to its display info, degrading gracefully for unknown codes. */
export const getLegalSource = (source: string): LegalSourceInfo => legalSources[source] ?? { label: source };
