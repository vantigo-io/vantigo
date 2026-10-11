/**
 * An employee number is at most 16 characters (Entra's `employeeId` bound),
 * so the settings' prefix and next number together may make no longer one
 * (`employee_number_too_long`). The number is generated unpadded: `EMP-` and 7
 * make `EMP-7`.
 */
export const EMPLOYEE_NUMBER_MAX_LENGTH = 16;
export const EMPLOYEE_NUMBER_PREFIX_MAX_LENGTH = 8;

/** Letters, digits and hyphens — what an employee number, its prefix and an employment number are made of. */
export const NUMBER_CHARACTERS = /^[A-Za-z0-9-]*$/;

/** The next number the settings would generate, before any skip past one already taken. */
export const nextGeneratedNumber = (prefix: string, next: number): string => `${prefix}${next}`;
