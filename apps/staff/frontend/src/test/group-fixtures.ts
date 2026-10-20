import type { StaffGroup, StaffGroupProvider, StaffGroupTraitDeclaration, StaffPersonGroup } from "../api/groups";

/**
 * Groups as the server answers them (Entra delivery D): a listed group with
 * two members and an owner, a derived one by a department and those beneath
 * it, and an archived one. Fresh copies per call: the fake writes to them.
 */
export const groups = (): StaffGroup[] => [
  {
    id: 1001,
    key: "engineering",
    name: "Engineering",
    description: "Everyone who builds the product",
    kind: "listed",
    archivedAt: undefined,
    memberCount: 2,
    ownerCount: 1,
    providers: [{ key: "entra", enabled: true, state: "synced" }],
    members: [
      {
        personId: 101,
        displayName: "Ada Lovelace",
        employeeNumber: "E-1",
        source: "listed",
        since: "2026-09-01",
      },
      {
        personId: 102,
        displayName: "Grace Hopper",
        employeeNumber: "E-2",
        source: "listed",
        since: "2026-10-01",
      },
    ],
    owners: [{ personId: 101, displayName: "Ada Lovelace", employeeNumber: "E-1" }],
    revision: 3,
    createdAt: "2026-09-01T08:00:00Z",
    updatedAt: "2026-10-01T08:00:00Z",
  },
  {
    id: 1002,
    key: "engineering-dept",
    name: "Engineering department",
    kind: "derived",
    rule: { kind: "department", id: 21, includeBelow: true },
    memberCount: 1,
    ownerCount: 0,
    providers: [],
    members: [
      {
        personId: 103,
        displayName: "Alan Turing",
        employeeNumber: "E-3",
        source: "derived",
        since: "2026-10-11",
      },
    ],
    owners: [],
    revision: 1,
    createdAt: "2026-10-10T08:00:00Z",
    updatedAt: "2026-10-10T08:00:00Z",
  },
  {
    id: 1003,
    key: "old-project",
    name: "Old project",
    kind: "listed",
    archivedAt: "2026-10-05T08:00:00Z",
    memberCount: 0,
    ownerCount: 0,
    providers: [{ key: "entra", enabled: true, state: "retired" }],
    members: [],
    owners: [],
    revision: 2,
    createdAt: "2026-01-10T08:00:00Z",
    updatedAt: "2026-10-05T08:00:00Z",
  },
];

/** A declaration with every type: a required choice with a default, a bounded string, a bool, a list. */
export const traitDeclarations = (): StaffGroupTraitDeclaration[] => [
  {
    key: "type",
    type: "choice",
    label: { en: "Group type", nb: "Gruppetype" },
    description: { en: "Fixed once the group is made." },
    choices: [
      { value: "security", label: { en: "Security", nb: "Sikkerhet" } },
      { value: "microsoft365", label: { en: "Microsoft 365" } },
    ],
    docUrl: "https://learn.microsoft.com/entra/fundamentals/concept-learn-about-groups",
    default: "security",
    required: true,
  },
  {
    key: "mailNickname",
    type: "string",
    label: { en: "Mail nickname", nb: "E-postkallenavn" },
    maxLength: 64,
    required: false,
  },
  { key: "team", type: "bool", label: { en: "Make it a team", nb: "Gjør den til et team" }, required: false },
  {
    key: "licences",
    type: "list",
    label: { en: "Licences" },
    choices: [
      { value: "ENTERPRISEPACK", label: { en: "ENTERPRISEPACK (5 of 25 available)" } },
      { value: "EMS", label: { en: "EMS (2 of 10 available)" } },
    ],
    required: false,
  },
];

/** Microsoft Entra ID's section of a group, off and never synced, unless the overrides say otherwise. */
export const entraSection = (overrides: Partial<StaffGroupProvider> = {}): StaffGroupProvider => ({
  key: "entra",
  name: "Microsoft Entra ID",
  status: "connected",
  enabled: false,
  traits: traitDeclarations(),
  values: {},
  ...overrides,
});

/** The sections the fake answers for the fixture's groups, keyed by the group's id. */
export const groupTraits = (): Record<number, StaffGroupProvider[]> => ({
  1001: [
    entraSection({
      enabled: true,
      values: { type: "security", mailNickname: "engineering", team: false, licences: ["EMS"] },
      state: "synced",
      externalId: "5b0f2b8c-0000-4000-8000-000000001001",
      syncedAt: "2026-10-11T06:00:00Z",
    }),
  ],
  1002: [entraSection()],
  1003: [
    entraSection({
      enabled: true,
      values: { type: "security" },
      state: "retired",
      syncedAt: "2026-10-04T06:00:00Z",
      retiredAt: "2026-10-06T06:00:00Z",
    }),
  ],
});

/** `GET /me/groups`: the caller's groups, by name alone. */
export const myGroups = (): StaffPersonGroup[] => [
  { id: 1001, name: "Engineering" },
  { id: 1002, name: "Engineering department" },
];
