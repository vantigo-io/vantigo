import { db } from "@vantigo/customers/database/db";
import { type Contact, contacts } from "@vantigo/customers/database/schema/contacts";
import { type Customer, customers } from "@vantigo/customers/database/schema/customers";
import { customersContacts } from "@vantigo/customers/database/schema/customers-contacts";
import { and, asc, count, desc, eq, ilike, or, type SQL, sql } from "drizzle-orm";
import type {
  ContactCreateInput,
  ContactListQuery,
  ContactUpdateInput,
  CustomerContactUpdateInput,
} from "./schemas";

const sortColumns = {
  name: contacts.name,
  email: contacts.email,
  createdAt: contacts.createdAt,
} as const;

export interface ContactListItem extends Contact {
  customerCount: number;
}

export interface ContactListResult {
  items: ContactListItem[];
  total: number;
  page: number;
  pageSize: number;
}

export async function listContacts(query: ContactListQuery): Promise<ContactListResult> {
  const filters: SQL[] = [];
  if (query.search) {
    const pattern = `%${query.search}%`;
    const searchFilter = or(
      ilike(contacts.name, pattern),
      ilike(contacts.email, pattern),
      ilike(contacts.phone, pattern),
    );
    if (searchFilter) filters.push(searchFilter);
  }

  const where = filters.length > 0 ? and(...filters) : undefined;
  const orderBy =
    query.sortDir === "desc" ? desc(sortColumns[query.sortBy]) : asc(sortColumns[query.sortBy]);

  const customerCount = db.$count(customersContacts, eq(customersContacts.contactId, contacts.id));

  const [items, [{ total }]] = await Promise.all([
    db
      .select({
        id: contacts.id,
        name: contacts.name,
        email: contacts.email,
        phone: contacts.phone,
        notes: contacts.notes,
        createdAt: contacts.createdAt,
        updatedAt: contacts.updatedAt,
        customerCount: sql<number>`${customerCount}`.mapWith(Number),
      })
      .from(contacts)
      .where(where)
      .orderBy(orderBy)
      .limit(query.pageSize)
      .offset((query.page - 1) * query.pageSize),
    db.select({ total: count() }).from(contacts).where(where),
  ]);

  return { items, total, page: query.page, pageSize: query.pageSize };
}

export interface ContactCustomerLink {
  customerId: number;
  legalName: Customer["legalName"];
  status: Customer["status"];
  role: string | null;
  isPrimary: boolean;
}

export interface ContactWithCustomers extends Contact {
  customers: ContactCustomerLink[];
}

export async function getContactWithCustomers(
  id: number,
): Promise<ContactWithCustomers | undefined> {
  const [contact] = await db.select().from(contacts).where(eq(contacts.id, id));
  if (!contact) return undefined;

  const links = await db
    .select({
      customerId: customersContacts.customerId,
      legalName: customers.legalName,
      status: customers.status,
      role: customersContacts.role,
      isPrimary: customersContacts.isPrimary,
    })
    .from(customersContacts)
    .innerJoin(customers, eq(customersContacts.customerId, customers.id))
    .where(eq(customersContacts.contactId, id))
    .orderBy(asc(customers.legalName));

  return { ...contact, customers: links };
}

export async function createContact(input: ContactCreateInput): Promise<Contact> {
  const [contact] = await db.insert(contacts).values(input).returning();
  return contact;
}

export async function updateContact(
  id: number,
  input: ContactUpdateInput,
): Promise<Contact | undefined> {
  const [contact] = await db.update(contacts).set(input).where(eq(contacts.id, id)).returning();
  return contact;
}

export async function deleteContact(id: number): Promise<boolean> {
  const deleted = await db.delete(contacts).where(eq(contacts.id, id)).returning({
    id: contacts.id,
  });
  return deleted.length > 0;
}

export interface CustomerContactItem extends Contact {
  role: string | null;
  isPrimary: boolean;
}

export async function listCustomerContacts(customerId: number): Promise<CustomerContactItem[]> {
  const rows = await db
    .select({
      id: contacts.id,
      name: contacts.name,
      email: contacts.email,
      phone: contacts.phone,
      notes: contacts.notes,
      createdAt: contacts.createdAt,
      updatedAt: contacts.updatedAt,
      role: customersContacts.role,
      isPrimary: customersContacts.isPrimary,
    })
    .from(customersContacts)
    .innerJoin(contacts, eq(customersContacts.contactId, contacts.id))
    .where(eq(customersContacts.customerId, customerId))
    .orderBy(desc(customersContacts.isPrimary), asc(contacts.name));

  return rows;
}

/** Links a contact; when marked primary, any existing primary is demoted first. */
export async function linkContact(
  customerId: number,
  contactId: number,
  input: { role?: string | null; isPrimary?: boolean },
): Promise<void> {
  await db.transaction(async (tx) => {
    if (input.isPrimary) {
      await tx
        .update(customersContacts)
        .set({ isPrimary: false })
        .where(eq(customersContacts.customerId, customerId));
    }
    await tx.insert(customersContacts).values({
      customerId,
      contactId,
      role: input.role ?? null,
      isPrimary: input.isPrimary ?? false,
    });
  });
}

export async function updateLink(
  customerId: number,
  contactId: number,
  input: CustomerContactUpdateInput,
): Promise<boolean> {
  return db.transaction(async (tx) => {
    if (input.isPrimary) {
      await tx
        .update(customersContacts)
        .set({ isPrimary: false })
        .where(eq(customersContacts.customerId, customerId));
    }
    const updated = await tx
      .update(customersContacts)
      .set(input)
      .where(
        and(
          eq(customersContacts.customerId, customerId),
          eq(customersContacts.contactId, contactId),
        ),
      )
      .returning({ contactId: customersContacts.contactId });
    return updated.length > 0;
  });
}

export async function unlinkContact(customerId: number, contactId: number): Promise<boolean> {
  const deleted = await db
    .delete(customersContacts)
    .where(
      and(eq(customersContacts.customerId, customerId), eq(customersContacts.contactId, contactId)),
    )
    .returning({ contactId: customersContacts.contactId });
  return deleted.length > 0;
}
