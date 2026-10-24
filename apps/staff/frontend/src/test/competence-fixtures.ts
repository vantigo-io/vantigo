import type { StaffCompetence, StaffCompetenceCategory, StaffPersonCompetence } from "../api/competences";

/** The five seeded categories (staff competence design S1), in order, and one of the company's own. */
export const competenceCategories = (): StaffCompetenceCategory[] => [
  ...(
    [
      [1001, "fag", "Trade", "Fag"],
      [1002, "sertifikat", "Certificate", "Sertifikat"],
      [1003, "sprak", "Language", "Språk"],
      [1004, "verktoy", "Tool", "Verktøy"],
      [1005, "domene", "Domain", "Domene"],
    ] as const
  ).map(([id, key, en, nb], index) => ({
    id,
    key,
    names: { en, nb },
    seeded: true,
    sort: index + 1,
    revision: 1,
  })),
  { id: 1006, key: "safety", names: { en: "Safety", nb: "Sikkerhet" }, seeded: false, sort: 6, revision: 2 },
];

const competence = (
  row: Omit<StaffCompetence, "seeded" | "hidden" | "sort" | "revision"> &
    Partial<Pick<StaffCompetence, "seeded" | "hidden" | "sort" | "revision">>,
): StaffCompetence => ({ seeded: false, hidden: false, sort: row.id - 2000, revision: 1, ...row });

/**
 * The catalogue as `staff:manage` reads it with the hidden and the archived:
 * three seeded certificates — FSE (12 months, warned 30 days ahead), hot work
 * and first aid, hidden — and the company's skills: welding and English with
 * a level, Excel without, and an archived one.
 */
export const competences = (): StaffCompetence[] => [
  competence({
    id: 2001,
    categoryId: 1002,
    key: "fse",
    names: { en: "Electrical safety (FSE)", nb: "Sikkerhet ved elektriske anlegg (FSE)" },
    kind: "certificate",
    hasLevel: false,
    validityMonths: 12,
    reminderLeadDays: 30,
    issuerHint: "The employer, by FSE § 7",
    seeded: true,
  }),
  competence({
    id: 2002,
    categoryId: 1002,
    key: "varme-arbeider",
    names: { en: "Hot work", nb: "Varme arbeider" },
    kind: "certificate",
    hasLevel: false,
    validityMonths: 60,
    reminderLeadDays: 180,
    seeded: true,
  }),
  competence({
    id: 2003,
    categoryId: 1002,
    key: "forstehjelp",
    names: { en: "First aid", nb: "Førstehjelp" },
    kind: "certificate",
    hasLevel: false,
    validityMonths: 24,
    reminderLeadDays: 60,
    seeded: true,
    hidden: true,
  }),
  competence({
    id: 2004,
    categoryId: 1001,
    key: "welding",
    names: { en: "Welding", nb: "Sveising" },
    kind: "skill",
    hasLevel: true,
  }),
  competence({
    id: 2005,
    categoryId: 1003,
    key: "english",
    names: { en: "English", nb: "Engelsk" },
    kind: "skill",
    hasLevel: true,
  }),
  competence({
    id: 2006,
    categoryId: 1004,
    key: "excel",
    names: { en: "Excel", nb: "Excel" },
    kind: "skill",
    hasLevel: false,
  }),
  competence({
    id: 2007,
    categoryId: 1004,
    key: "lotus",
    names: { en: "Lotus 1-2-3", nb: "Lotus 1-2-3" },
    kind: "skill",
    hasLevel: false,
    archivedAt: "2026-01-01T09:00:00Z",
  }),
];

const summaryOf = (id: number): StaffPersonCompetence["competence"] => {
  const row = competences().find((one) => one.id === id) as StaffCompetence;
  return {
    id: row.id,
    key: row.key,
    names: row.names,
    kind: row.kind,
    hasLevel: row.hasLevel,
    categoryId: row.categoryId,
    archived: row.archivedAt !== undefined,
  };
};

/**
 * Every person's rows as HR reads them, with every detail field; the fake
 * shapes them per reader and works out `expiring` and `expired` from today.
 * Ada holds a verified FSE certificate that expires within its 30 days and
 * a declared welding skill; Grace has declared English.
 */
export const personCompetences = (): StaffPersonCompetence[] => [
  {
    id: 3001,
    personId: 101,
    competence: summaryOf(2001),
    validFrom: "2025-11-01",
    validTo: "2026-11-01",
    issuedOn: "2025-11-01",
    issuer: "Fjord Systems AS",
    reference: "FSE-2025-17",
    evidence: { documentId: 503, fileName: "fse-course.png", contentType: "image/png", sizeBytes: 2_400_000 },
    source: "self",
    verified: true,
    verifiedAt: "2025-11-03T09:00:00Z",
    verifiedBy: "Hedy Lamarr",
    verifiedBasis: "evidence",
    verifiedNote: "Saw the course certificate",
    superseded: false,
    expiring: false,
    expired: false,
    declaredAt: "2025-11-02T08:00:00Z",
    revision: 2,
  },
  {
    id: 3002,
    personId: 101,
    competence: summaryOf(2004),
    level: 2,
    validFrom: "2026-09-01",
    source: "self",
    verified: false,
    superseded: false,
    expiring: false,
    expired: false,
    declaredAt: "2026-09-01T08:00:00Z",
    revision: 1,
  },
  {
    id: 3003,
    personId: 102,
    competence: summaryOf(2005),
    level: 3,
    validFrom: "2026-10-01",
    source: "self",
    verified: false,
    superseded: false,
    expiring: false,
    expired: false,
    declaredAt: "2026-10-01T08:00:00Z",
    revision: 1,
  },
];
