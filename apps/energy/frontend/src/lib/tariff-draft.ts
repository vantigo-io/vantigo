import { randomId } from "@mantine/hooks";
import type {
  Tariff,
  TariffComponent,
  TariffComponentInput,
  TariffComponentKind,
  TariffInput,
  TariffKind,
} from "../api/tariffs";

/**
 * The tariff editor's draft (energy tariffs plan, reading 3): what the form
 * holds, how it becomes the request — every field a kind does not use sent
 * as null — and where the server's 400 keys land on it.
 */

export type Num = number | "";

export interface TierDraft {
  key: string;
  fromKw: Num;
  perMonth: Num;
}

export interface ComponentDraft {
  key: string;
  kind: TariffComponentKind;
  name: string;
  rate: Num;
  /** grid_energy only: whether the night rate, its day window and weekdays-only are set — all or none. */
  hasNight: boolean;
  rateNight: Num;
  dayFrom: Num;
  dayTo: Num;
  weekdaysOnly: boolean;
  tiers: TierDraft[];
  rateReduced: Num;
  rateBusiness: Num;
}

export interface VersionDraft {
  key: string;
  validFrom: string;
  /** The first day the version no longer applies; empty is open-ended. */
  validTo: string;
  components: ComponentDraft[];
}

export interface TariffDraft {
  kind: TariffKind;
  name: string;
  description: string;
  versions: VersionDraft[];
}

/** The rate fields each kind uses (reading 3's table); grid_energy's night fields are its switch's. */
export const RATE_FIELDS: Record<TariffComponentKind, readonly ("rate" | "rateReduced" | "rateBusiness")[]> = {
  spot_energy: [],
  fixed_energy: ["rate"],
  markup: ["rate"],
  monthly_fee: ["rate"],
  grid_fixed: ["rate"],
  grid_energy: ["rate"],
  grid_capacity: [],
  electricity_tax: ["rate", "rateReduced"],
  enova_levy: ["rate", "rateBusiness"],
};

/** Kinds a version may hold once; a power version holds exactly one of the two energy kinds. */
export const SINGLE_KINDS: readonly TariffComponentKind[] = [
  "grid_energy",
  "grid_capacity",
  "electricity_tax",
  "enova_levy",
];
export const ENERGY_KINDS: readonly TariffComponentKind[] = ["spot_energy", "fixed_energy"];

export const pascal = (kind: string) =>
  kind
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join("");

/** The i18n key of a component kind's name: `tariffComponentSpotEnergy`. */
export const componentKindKey = (kind: TariffComponentKind) => `tariffComponent${pascal(kind)}`;

/** The label of a component's field, with its unit: `tariffFieldRateMarkup`. */
export const rateLabelKey = (kind: TariffComponentKind, field: "rate" | "rateReduced" | "rateBusiness") =>
  `tariffField${pascal(field)}${pascal(kind)}`;

export const newComponent = (kind: TariffComponentKind, name: string): ComponentDraft => ({
  key: randomId(),
  kind,
  name,
  rate: "",
  hasNight: false,
  rateNight: "",
  dayFrom: "",
  dayTo: "",
  weekdaysOnly: false,
  tiers: kind === "grid_capacity" ? [{ key: randomId(), fromKw: 0, perMonth: "" }] : [],
  rateReduced: "",
  rateBusiness: "",
});

export const newTier = (): TierDraft => ({ key: randomId(), fromKw: "", perMonth: "" });

export const newVersion = (validFrom = "", components: ComponentDraft[] = []): VersionDraft => ({
  key: randomId(),
  validFrom,
  validTo: "",
  components,
});

const orEmpty = (value: number | null): Num => value ?? "";

const componentDraft = (component: TariffComponent): ComponentDraft => ({
  key: randomId(),
  kind: component.kind,
  name: component.name,
  rate: orEmpty(component.rate),
  hasNight: component.rateNight !== null,
  rateNight: orEmpty(component.rateNight),
  dayFrom: orEmpty(component.dayFrom),
  dayTo: orEmpty(component.dayTo),
  weekdaysOnly: component.weekdaysOnly,
  tiers: (component.tiers ?? []).map((tier) => ({ key: randomId(), fromKw: tier.fromKw, perMonth: tier.perMonth })),
  rateReduced: orEmpty(component.rateReduced),
  rateBusiness: orEmpty(component.rateBusiness),
});

export const draftFromTariff = (tariff: Tariff): TariffDraft => ({
  kind: tariff.kind,
  name: tariff.name,
  description: tariff.description ?? "",
  versions: tariff.versions.map((version) => ({
    key: randomId(),
    validFrom: version.validFrom,
    validTo: version.validTo ?? "",
    components: version.components.map(componentDraft),
  })),
});

const num = (value: Num) => (value === "" ? null : Number(value));

/** A component as the server takes it: every field its kind does not use is null. */
export const componentInput = (draft: ComponentDraft): TariffComponentInput => {
  const fields = RATE_FIELDS[draft.kind];
  const night = draft.kind === "grid_energy" && draft.hasNight;
  return {
    kind: draft.kind,
    name: draft.name.trim(),
    rate: fields.includes("rate") ? num(draft.rate) : null,
    rateNight: night ? num(draft.rateNight) : null,
    dayFrom: night ? num(draft.dayFrom) : null,
    dayTo: night ? num(draft.dayTo) : null,
    weekdaysOnly: night ? draft.weekdaysOnly : null,
    tiers:
      draft.kind === "grid_capacity"
        ? draft.tiers.map((tier) => ({ fromKw: Number(tier.fromKw), perMonth: Number(tier.perMonth) }))
        : null,
    rateReduced: fields.includes("rateReduced") ? num(draft.rateReduced) : null,
    rateBusiness: fields.includes("rateBusiness") ? num(draft.rateBusiness) : null,
  };
};

export const tariffInput = (draft: TariffDraft): TariffInput => ({
  kind: draft.kind,
  name: draft.name.trim(),
  description: draft.description.trim() || null,
  versions: draft.versions.map((version) => ({
    validFrom: version.validFrom,
    validTo: version.validTo || null,
    components: version.components.map(componentInput),
  })),
});

/** The server's `versions[1].components[2].rate` as the form's path, `versions.1.components.2.rate`. */
export const formPath = (key: string) => key.replace(/\[(\d+)\]/g, ".$1").replace(/^\./, "");

/** Every path the editor shows an error on, for the draft as it stands. */
export const placeablePaths = (draft: TariffDraft): Set<string> => {
  const paths = new Set(["kind", "name", "description", "versions"]);
  draft.versions.forEach((version, i) => {
    const v = `versions.${i}`;
    for (const path of [v, `${v}.validFrom`, `${v}.validTo`, `${v}.components`]) paths.add(path);
    version.components.forEach((component, j) => {
      const c = `${v}.components.${j}`;
      for (const path of [c, `${c}.kind`, `${c}.name`]) paths.add(path);
      for (const field of RATE_FIELDS[component.kind]) paths.add(`${c}.${field}`);
      if (component.kind === "grid_energy" && component.hasNight)
        for (const field of ["rateNight", "dayFrom", "dayTo", "weekdaysOnly"]) paths.add(`${c}.${field}`);
      if (component.kind === "grid_capacity") {
        paths.add(`${c}.tiers`);
        component.tiers.forEach((_, k) => {
          paths.add(`${c}.tiers.${k}.fromKw`);
          paths.add(`${c}.tiers.${k}.perMonth`);
        });
      }
    });
  });
  return paths;
};

export const emptyDraft = (kind: TariffKind, spotName: string): TariffDraft => ({
  kind,
  name: "",
  description: "",
  versions: [newVersion("", kind === "power" ? [newComponent("spot_energy", spotName)] : [])],
});
