import type { StaffMeInput } from "../api/me";
import type { StaffNextOfKin, StaffNextOfKinRelationship, StaffPerson, StaffPersonInput } from "../api/people";
import { EMPLOYEE_NUMBER_MAX_LENGTH, NUMBER_CHARACTERS } from "./employee-number";

/** A person's fields as the form holds them: every text a string, an unset choice null. */
export interface PersonFormValues {
  givenName: string;
  familyName: string;
  displayName: string;
  /** Off, the employee number is generated from the settings' prefix and next number. */
  assignOwnNumber: boolean;
  employeeNumber: string;
  workEmail: string;
  phoneWork: string;
  workCountry: string;
  preferredLanguage: "en" | "nb";
  birthDate: string | null;
  gender: StaffPersonInput["gender"] | null;
  phoneMobile: string;
  phonePrivate: string;
  privateEmail: string;
  street: string;
  postalCode: string;
  city: string;
  country: string;
  /** HR's notes — read and written by `staff:manage` and `staff:personal-data` alone. */
  notes: string;
}

export const emptyPerson = (): PersonFormValues => ({
  givenName: "",
  familyName: "",
  displayName: "",
  assignOwnNumber: false,
  employeeNumber: "",
  workEmail: "",
  phoneWork: "",
  workCountry: "",
  // The server's own default when the field is left out, made visible.
  preferredLanguage: "nb",
  birthDate: null,
  gender: null,
  phoneMobile: "",
  phonePrivate: "",
  privateEmail: "",
  street: "",
  postalCode: "",
  city: "",
  country: "",
  notes: "",
});

/**
 * A person as HR's edit form opens them: every field the response carries,
 * an absent one empty. The employee number is shown, never sent — it does
 * not change after the create — so the switch that would send it stays off.
 */
export const personFormValuesOf = (person: StaffPerson): PersonFormValues => ({
  givenName: person.givenName,
  familyName: person.familyName,
  displayName: person.displayName,
  assignOwnNumber: false,
  employeeNumber: person.employeeNumber,
  workEmail: person.workEmail ?? "",
  phoneWork: person.phoneWork ?? "",
  workCountry: person.workCountry,
  preferredLanguage: person.preferredLanguage,
  birthDate: person.birthDate ?? null,
  gender: person.gender ?? null,
  phoneMobile: person.phoneMobile ?? "",
  phonePrivate: person.phonePrivate ?? "",
  privateEmail: person.privateEmail ?? "",
  street: person.street ?? "",
  postalCode: person.postalCode ?? "",
  city: person.city ?? "",
  country: person.country ?? "",
  notes: person.notes ?? "",
});

/** The private group (design §5): optional at the create, the person's own and HR's to read. */
export const privateFields = [
  "birthDate",
  "gender",
  "phoneMobile",
  "phonePrivate",
  "privateEmail",
  "street",
  "postalCode",
  "city",
  "country",
] as const;

/** Every field the form shows, as the server keys its field errors. */
export const personFields = [
  "givenName",
  "familyName",
  "displayName",
  "employeeNumber",
  "workEmail",
  "phoneWork",
  "workCountry",
  "preferredLanguage",
  ...privateFields,
  "notes",
] as const;

const text = <K extends string>(key: K, value: string): Partial<Record<K, string>> =>
  value.trim() ? ({ [key]: value.trim() } as Record<K, string>) : {};

/** The request: trimmed, an empty optional left out, a country upper-cased. */
export const personInputOf = (values: PersonFormValues): StaffPersonInput => ({
  givenName: values.givenName.trim(),
  familyName: values.familyName.trim(),
  ...text("displayName", values.displayName),
  ...(values.assignOwnNumber ? text("employeeNumber", values.employeeNumber) : {}),
  ...text("workEmail", values.workEmail),
  ...text("phoneWork", values.phoneWork),
  ...(values.workCountry.trim() ? { workCountry: values.workCountry.trim().toUpperCase() } : {}),
  preferredLanguage: values.preferredLanguage,
  ...(values.birthDate ? { birthDate: values.birthDate } : {}),
  ...(values.gender ? { gender: values.gender } : {}),
  ...text("phoneMobile", values.phoneMobile),
  ...text("phonePrivate", values.phonePrivate),
  ...text("privateEmail", values.privateEmail),
  ...text("street", values.street),
  ...text("postalCode", values.postalCode),
  ...text("city", values.city),
  ...(values.country.trim() ? { country: values.country.trim().toUpperCase() } : {}),
  ...text("notes", values.notes),
});

/** One of the next of kin as a form holds them: the texts as strings, the relationship unchosen as null. */
export interface NextOfKinFormValues {
  name: string;
  relationship: StaffNextOfKinRelationship | null;
  phone: string;
  note: string;
}

export const emptyNextOfKin = (): NextOfKinFormValues => ({ name: "", relationship: null, phone: "", note: "" });

export const nextOfKinFormValuesOf = (kin: StaffNextOfKin): NextOfKinFormValues => ({
  name: kin.name,
  relationship: kin.relationship,
  phone: kin.phone ?? "",
  note: kin.note ?? "",
});

/** The list as the API takes it: in order, trimmed, an empty optional left out. */
export const nextOfKinInputOf = (rows: NextOfKinFormValues[]): StaffNextOfKin[] =>
  rows.map((row) => ({
    name: row.name.trim(),
    relationship: row.relationship as StaffNextOfKinRelationship,
    ...text("phone", row.phone),
    ...text("note", row.note),
  }));

/** The checks of one row of next of kin, for a form whose list sits under `nextOfKin`. */
export const nextOfKinValidation = (t: Translate) => ({
  name: (value: string) => (value.trim() ? maxLength(t, 200)(value) : t("nameRequired")),
  relationship: (value: StaffNextOfKinRelationship | null) => (value ? null : t("relationshipRequired")),
  phone: maxLength(t, 32),
  note: maxLength(t, 500),
});

/** What the person edits on My page (design S17): the private phones and email, the address, the next of kin. */
export interface MyDetailsFormValues {
  phoneMobile: string;
  phonePrivate: string;
  privateEmail: string;
  street: string;
  postalCode: string;
  city: string;
  country: string;
  nextOfKin: NextOfKinFormValues[];
}

export const myDetailsFormValuesOf = (person: StaffPerson, nextOfKin: StaffNextOfKin[]): MyDetailsFormValues => ({
  phoneMobile: person.phoneMobile ?? "",
  phonePrivate: person.phonePrivate ?? "",
  privateEmail: person.privateEmail ?? "",
  street: person.street ?? "",
  postalCode: person.postalCode ?? "",
  city: person.city ?? "",
  country: person.country ?? "",
  nextOfKin: nextOfKin.map(nextOfKinFormValuesOf),
});

/** `PUT /me`'s body: exactly the fields it names — a full replace, an empty optional left out and so cleared. */
export const meInputOf = (values: MyDetailsFormValues): StaffMeInput => ({
  ...text("phoneMobile", values.phoneMobile),
  ...text("phonePrivate", values.phonePrivate),
  ...text("privateEmail", values.privateEmail),
  ...text("street", values.street),
  ...text("postalCode", values.postalCode),
  ...text("city", values.city),
  ...(values.country.trim() ? { country: values.country.trim().toUpperCase() } : {}),
  nextOfKin: nextOfKinInputOf(values.nextOfKin),
});

export const myDetailsFields = [
  "phoneMobile",
  "phonePrivate",
  "privateEmail",
  "street",
  "postalCode",
  "city",
  "country",
] as const;

type Translate = (key: string, options?: Record<string, unknown>) => string;

const country = (t: Translate) => (value: string) =>
  value.trim() === "" || /^[A-Za-z]{2}$/.test(value.trim()) ? null : t("countryInvalid");
const email = (t: Translate) => (value: string) =>
  value.trim() === "" || /^[^\s@]+@[^\s@]+$/.test(value.trim()) ? null : t("emailInvalid");
const maxLength = (t: Translate, max: number) => (value: string) =>
  value.trim().length > max ? t("textTooLong", { max }) : null;

/**
 * The checks the form makes before asking: what is required and what is
 * obviously malformed. The server judges everything again — uniqueness, the
 * generated number's length — and its field errors land on the same fields.
 */
export const personValidation = (t: Translate) => ({
  givenName: (value: string) => (value.trim() ? maxLength(t, 100)(value) : t("givenNameRequired")),
  familyName: (value: string) => (value.trim() ? maxLength(t, 100)(value) : t("familyNameRequired")),
  displayName: maxLength(t, 200),
  employeeNumber: (value: string, values: PersonFormValues) => {
    if (!values.assignOwnNumber) return null;
    if (!value.trim()) return t("employeeNumberRequired");
    return value.trim().length > EMPLOYEE_NUMBER_MAX_LENGTH || !NUMBER_CHARACTERS.test(value.trim())
      ? t("employeeNumberInvalid", { max: EMPLOYEE_NUMBER_MAX_LENGTH })
      : null;
  },
  workEmail: email(t),
  privateEmail: email(t),
  phoneWork: maxLength(t, 32),
  phoneMobile: maxLength(t, 32),
  phonePrivate: maxLength(t, 32),
  workCountry: country(t),
  country: country(t),
  notes: maxLength(t, 2000),
});

/** The checks My page makes before asking; the server judges them again. */
export const myDetailsValidation = (t: Translate) => ({
  privateEmail: email(t),
  phoneMobile: maxLength(t, 32),
  phonePrivate: maxLength(t, 32),
  street: maxLength(t, 200),
  postalCode: maxLength(t, 16),
  city: maxLength(t, 100),
  country: country(t),
  nextOfKin: nextOfKinValidation(t),
});
