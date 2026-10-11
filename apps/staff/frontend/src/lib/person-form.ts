import type { StaffPersonInput } from "../api/people";
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
});

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
});
