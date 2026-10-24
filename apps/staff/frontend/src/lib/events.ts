/**
 * The kinds of event the server writes in this delivery (design S21), one to
 * one with `events.go`, each named by a catalog key `event_<kind>`. A kind
 * the server adds later still renders — as its code — rather than vanishing.
 */
export const eventKinds = [
  "person_created",
  "person_updated",
  "next_of_kin_updated",
  "roles_updated",
  // Installation-level: a deleted person has no history to show it in, so it
  // is never in a person's history; it is here so the kinds match the server's.
  "person_deleted",
  "national_id_set",
  "national_id_revealed",
  "bank_account_set",
  "bank_account_revealed",
  "user_linked",
  "user_unlinked",
  "employment_created",
  "employment_updated",
  "employment_ended",
  "employment_reopened",
  "terms_added",
  "terms_corrected",
  "terms_deleted",
  "document_added",
  "document_updated",
  "document_removed",
  "personal_data_exported",
  "legal_hold_set",
  "legal_hold_lifted",
  "reassessed",
  "person_rehired",
  // Installation-level, like person_deleted: the register's import and export
  // (plan reading 11) belong to no person's history.
  "people_imported",
  "people_exported",
  // Installation-level too: a month's payroll file (plan reading 13).
  "payroll_exported",
  // The retention run's erase of a person (design S15): its payload names
  // no field, only the kinds erased and their counts.
  "erased",
  // Installation-level: a threshold acknowledged (design S20), naming its key.
  "threshold_acknowledged",
  // The account (delivery C, `account_flag.go`): HR's change of the flag, by
  // its fields, and the accounts worker's steps, by their codes (accountLines).
  "account_flag_updated",
  "account_prepared",
  "account_activated",
  "account_deactivated",
  "account_updated",
  "account_blocked",
  "account_failed",
  "account_mismatched",
  "identity_erased",
  // The provider link (Entra delivery B, `accounts_provider.go`): the
  // announcement and the provider's steps, by their codes (accountLines),
  // the address a provider made — never the address itself — and a pass
  // shown, naming the viewer and never the pass.
  "account_announced",
  "account_external_prepared",
  "account_external_activated",
  "account_external_deactivated",
  "account_external_updated",
  "account_external_deleted",
  "account_external_blocked",
  "account_external_failed",
  "work_email_set_by_provider",
  "pass_revealed",
  // A group joined or left (Entra delivery D, `groups_members.go`): HR's
  // write of a listed group's members, or the rule's materialisation,
  // naming the group and how the person came in or went (groupLine).
  "group_joined",
  "group_left",
  // Competence (staff phase 2, delivery A): a person's row declared or added,
  // changed, verified, renewed, removed and given its evidence, by its
  // fields; and the catalogue's two installation events.
  "competence_declared",
  "competence_changed",
  "competence_verified",
  "competence_renewed",
  "competence_removed",
  "competence_evidence_added",
  "competence_created",
  "competence_updated",
] as const;

/**
 * The catalog key that names a field of a payload — the contract's own
 * field names, which are mostly the form's labels already. Where the form's
 * key differs (a terms row's `title` is the job title), it is said here.
 */
const fieldLabelOverrides: Record<string, string> = {
  title: "jobTitle",
  note: "termsNote",
  notes: "hrNotes",
  departmentId: "department",
  locationId: "location",
  managerPersonId: "manager",
  businessUnitId: "businessUnit",
  user: "linkedUser",
  nationalId: "nationalId",
  bankAccount: "bankAccount",
  nextOfKin: "nextOfKin",
  roles: "roles",
  // The select's label, whose values are the kinds' short names.
  accountKind: "accountKindChoice",
  // A competence row's fields (phase 2): the labels the tab shows, whose
  // values — the source, the basis — are named under the same keys.
  competenceKey: "competencePick",
  level: "competenceLevel",
  issuedOn: "competenceIssuedOn",
  issuer: "competenceIssuer",
  reference: "competenceReference",
  source: "competenceSource",
  basis: "competenceBasis",
  documentId: "competenceEvidence",
  chain: "removeCompetenceChain",
};

export const eventFieldLabelKey = (field: string): string => fieldLabelOverrides[field] ?? field;

/** Every field a payload of this delivery can name — the person's, the employment's and a terms row's. */
export const eventFields = [
  "employeeNumber",
  "givenName",
  "familyName",
  "displayName",
  "workEmail",
  "phoneWork",
  "workCountry",
  "preferredLanguage",
  "birthDate",
  "gender",
  "phoneMobile",
  "phonePrivate",
  "privateEmail",
  "street",
  "postalCode",
  "city",
  "country",
  "notes",
  "nextOfKin",
  "roles",
  "nationalId",
  "bankAccount",
  "user",
  "employmentNumber",
  "startDate",
  "endDate",
  "endReason",
  "businessUnitId",
  "relationshipType",
  "contractSignedAt",
  "validFrom",
  "employmentType",
  "temporaryGround",
  "expectedEndDate",
  "appointmentForm",
  "hiredOutTo",
  "ftePercent",
  "workingHoursScheme",
  "fullTimeHoursPerWeek",
  "departmentId",
  "locationId",
  "managerPersonId",
  "title",
  "occupationCode",
  "employeeType",
  "costCentre",
  "division",
  "probationEndDate",
  "probationExtensionAgreed",
  "probationExtendedTo",
  "probationExtensionReason",
  "noticeEmployee",
  "noticeEmployer",
  "noticeUnit",
  "workingTimeText",
  "breakMinutes",
  "specialHoursText",
  "overtimeRuleText",
  "tariffReference",
  "holidayTermsText",
  "competenceDevelopmentText",
  "socialSecurityText",
  "remunerationType",
  "payComponents",
  "payMethod",
  "paydayText",
  "seniorityDate",
  "payGrade",
  "note",
  "legalHold",
  "legalHoldReason",
  "erasedAt",
  "reassessedAt",
  // The account flag's three (delivery C).
  "provisionAccount",
  "provisionFrom",
  "accountEmailKind",
  // The person's account kind (Entra delivery B), a change of the flag's.
  "accountKind",
  // A competence row's (phase 2); validFrom and validTo are the terms' words.
  "competenceKey",
  "level",
  "issuedOn",
  "issuer",
  "reference",
  "source",
  "basis",
  "documentId",
  "chain",
] as const;

/** What one field of a payload says: its values before and after, or only that it changed. */
export type FieldChange = "changed" | { from?: unknown; to?: unknown };

export interface EventFormatting {
  /** A catalog sentence. */
  t: (key: string, options?: Record<string, unknown>) => string;
  /** Whether the catalog has a key — an enumeration's value is named when it does. */
  has: (key: string) => boolean;
  date: (value: string) => string;
  number: (value: number) => string;
  /** The name of a row a field points at by id — a department, a location, a unit, a person — when the page knows it. */
  name: (field: string, id: number) => string | undefined;
}

/** The fields that point at another row by id: a person reads them by name, never by number. */
export const referenceFields: ReadonlySet<string> = new Set([
  "departmentId",
  "locationId",
  "businessUnitId",
  "managerPersonId",
]);

const ISO_DAY = /^\d{4}-\d{2}-\d{2}$/;

/**
 * One value of a payload as the reader reads it: a day as a date, a code by
 * its label when the catalog names it (`employmentType` `fast` is
 * "Permanent (fast)"), a list item by item, and the few shapes the module
 * writes — a user, a sealed field's kind and last four, a pay component, a
 * role, a next of kin — by what a person recognises them by.
 */
export const formatEventValue = (field: string, value: unknown, f: EventFormatting): string => {
  if (value === undefined || value === null || value === "") return "—";
  if (typeof value === "boolean") return f.t(value ? "yes" : "no");
  // A competence's level is named on the one scale (design S2), not counted.
  if (typeof value === "number") return field === "level" ? f.t(`level_${value}`) : f.number(value);
  if (typeof value === "string") {
    if (ISO_DAY.test(value)) return f.date(value);
    const enumKey = `${eventFieldLabelKey(field)}_${value}`;
    return f.has(enumKey) ? f.t(enumKey) : value;
  }
  if (Array.isArray(value))
    return value.length === 0 ? "—" : value.map((one) => formatEventValue(field, one, f)).join(", ");
  if (typeof value === "object") {
    const shape = value as Record<string, unknown>;
    if (typeof shape.displayName === "string") return shape.displayName;
    if (typeof shape.role === "string") return f.t(`role_${shape.role}`);
    if (typeof shape.name === "string" && typeof shape.amount === "number")
      return f.t("payComponentLine", {
        name: shape.name,
        amount: f.number(shape.amount),
        currency: shape.currency,
        period: f.t(`payPeriod_${shape.period}`),
      });
    if (typeof shape.name === "string") return shape.name;
    if (typeof shape.last4 === "string")
      return `${f.t(`bankAccountKind_${shape.kind}`)}, ${f.t("endingIn", { last4: shape.last4 })}`;
    if (typeof shape.kind === "string")
      return [f.t(`nationalIdKind_${shape.kind}`), shape.country].filter(Boolean).join(", ");
  }
  return String(value);
};

/** A code in words when the catalog has them under `prefix`, else the code itself. */
const coded = (prefix: string, code: string, f: EventFormatting) =>
  f.has(`${prefix}${code}`) ? f.t(`${prefix}${code}`) : code;

/**
 * An accounts worker's event (delivery C) names no field: its step, the
 * link's state after it, the reason or failure code, the provider's
 * reported codes, the invitation and whether the flag was turned off
 * meanwhile — each said in words.
 */
const accountLines = (payload: Record<string, unknown>, f: EventFormatting): string[] => {
  const lines: string[] = [];
  if (typeof payload.step === "string")
    lines.push(f.t("accountEventStep", { step: coded("accountStep_", payload.step, f) }));
  if (typeof payload.state === "string" && payload.state)
    lines.push(f.t("accountEventState", { state: coded("accountLinkState_", payload.state, f) }));
  if (typeof payload.code === "string" && payload.code) lines.push(coded("accountReason_", payload.code, f));
  // A provider link's reported codes (Entra delivery B), each in words; a
  // licence's code carries the SKU after a colon, which the words leave out.
  if (Array.isArray(payload.codes))
    for (const code of payload.codes)
      if (typeof code === "string" && code) lines.push(coded("accountReason_", code.split(":")[0], f));
  const invitation = payload.invitation as { state?: unknown; issued?: unknown } | undefined;
  if (invitation && typeof invitation.state === "string")
    lines.push(
      f.t("accountEventInvitation", {
        invitation: `${coded("accountInvitation_", invitation.state, f)} · ${f.t("invitationsIssued", {
          count: typeof invitation.issued === "number" ? invitation.issued : 0,
        })}`,
      }),
    );
  if (payload.flagOffMeanwhile === true) lines.push(f.t("accountEventFlagOffMeanwhile"));
  return lines;
};

/** A group event's one line: the group by name, and whether HR listed the person or the rule did. */
const groupLine = (payload: Record<string, unknown>, f: EventFormatting): string =>
  f.t("groupEventLine", {
    name: payload.groupName,
    source: typeof payload.source === "string" ? coded("groupSource_", payload.source, f) : "—",
  });

/** The lines an event's payload says, one per field, in the order the payload names them. */
export const eventLines = (payload: Record<string, unknown>, f: EventFormatting): string[] => {
  if (typeof payload.step === "string") return accountLines(payload, f);
  if (typeof payload.groupName === "string") return [groupLine(payload, f)];
  const fields = payload.fields;
  if (!fields || typeof fields !== "object") return [];
  return Object.entries(fields as Record<string, FieldChange>).map(([field, change]) => {
    const label = f.has(eventFieldLabelKey(field)) ? f.t(eventFieldLabelKey(field)) : field;
    if (change === "changed") return f.t("fieldChanged", { field: label });
    if (referenceFields.has(field)) {
      // A reference the page cannot name is said to have changed; its
      // number means nothing to the reader and is never shown.
      const named = (id: unknown) =>
        id === undefined || id === null ? "—" : typeof id === "number" ? f.name(field, id) : undefined;
      const from = named(change?.from);
      const to = named(change?.to);
      return from === undefined || to === undefined
        ? f.t("fieldChanged", { field: label })
        : f.t("fieldFromTo", { field: label, from, to });
    }
    return f.t("fieldFromTo", {
      field: label,
      from: formatEventValue(field, change?.from, f),
      to: formatEventValue(field, change?.to, f),
    });
  });
};
