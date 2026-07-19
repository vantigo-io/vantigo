import { db } from "@vantigo/customers/database/db";
import { type Customer, customers } from "@vantigo/customers/database/schema/customers";
import { and, asc, count, desc, eq, gte, ilike, ne, or, type SQL } from "drizzle-orm";
import type { CustomerCreateInput, CustomerListQuery, CustomerUpdateInput } from "./schemas";

const sortColumns = {
  id: customers.id,
  legalName: customers.legalName,
  legalType: customers.legalType,
  email: customers.email,
  status: customers.status,
  createdAt: customers.createdAt,
} as const;

export interface CustomerListResult {
  items: Customer[];
  total: number;
  page: number;
  pageSize: number;
}

export async function listCustomers(query: CustomerListQuery): Promise<CustomerListResult> {
  const filters: SQL[] = [];

  if (query.search) {
    const pattern = `%${query.search}%`;
    const searchFilter = or(
      ilike(customers.legalName, pattern),
      ilike(customers.email, pattern),
      ilike(customers.legalId, pattern),
    );
    if (searchFilter) filters.push(searchFilter);
  }
  if (query.legalType) filters.push(eq(customers.legalType, query.legalType));
  if (query.status) filters.push(eq(customers.status, query.status));
  if (query.legalId) filters.push(eq(customers.legalId, query.legalId));
  if (query.email) filters.push(eq(customers.email, query.email));

  const where = filters.length > 0 ? and(...filters) : undefined;
  const orderBy =
    query.sortDir === "desc" ? desc(sortColumns[query.sortBy]) : asc(sortColumns[query.sortBy]);

  const [items, [{ total }]] = await Promise.all([
    db
      .select()
      .from(customers)
      .where(where)
      .orderBy(orderBy)
      .limit(query.pageSize)
      .offset((query.page - 1) * query.pageSize),
    db.select({ total: count() }).from(customers).where(where),
  ]);

  return { items, total, page: query.page, pageSize: query.pageSize };
}

export async function getCustomerById(id: number): Promise<Customer | undefined> {
  const [customer] = await db.select().from(customers).where(eq(customers.id, id));
  return customer;
}

export async function createCustomer(input: CustomerCreateInput): Promise<Customer> {
  const [customer] = await db
    .insert(customers)
    .values({ ...input, notes: input.notes ?? null })
    .returning();
  return customer;
}

export async function updateCustomer(
  id: number,
  input: CustomerUpdateInput,
): Promise<Customer | undefined> {
  const [customer] = await db.update(customers).set(input).where(eq(customers.id, id)).returning();
  return customer;
}

export async function archiveCustomer(id: number): Promise<Customer | undefined> {
  return updateCustomer(id, { status: "archived" });
}

export interface DuplicateWarning {
  code: "DUPLICATE_LEGAL_ID" | "DUPLICATE_EMAIL";
  existingId: number;
}

/** Duplicates are warnings, never constraints: writes always succeed. */
export async function findDuplicateWarnings(
  input: { legalId?: string | null; email?: string | null },
  excludeId?: number,
): Promise<DuplicateWarning[]> {
  const warnings: DuplicateWarning[] = [];

  const check = async (
    column: typeof customers.legalId | typeof customers.email,
    value: string,
    code: DuplicateWarning["code"],
  ) => {
    const where = excludeId
      ? and(eq(column, value), ne(customers.id, excludeId))
      : eq(column, value);
    const [existing] = await db.select({ id: customers.id }).from(customers).where(where).limit(1);
    if (existing) warnings.push({ code, existingId: existing.id });
  };

  if (input.legalId) await check(customers.legalId, input.legalId, "DUPLICATE_LEGAL_ID");
  if (input.email) await check(customers.email, input.email, "DUPLICATE_EMAIL");

  return warnings;
}

export interface CustomerStats {
  total: number;
  active: number;
  newThisMonth: number;
  businessCount: number;
  privateCount: number;
}

export async function getCustomerStats(): Promise<CustomerStats> {
  const startOfMonth = new Date();
  startOfMonth.setDate(1);
  startOfMonth.setHours(0, 0, 0, 0);

  const [[{ total }], [{ active }], [{ newThisMonth }], [{ businessCount }], [{ privateCount }]] =
    await Promise.all([
      db.select({ total: count() }).from(customers),
      db.select({ active: count() }).from(customers).where(eq(customers.status, "active")),
      db
        .select({ newThisMonth: count() })
        .from(customers)
        .where(gte(customers.createdAt, startOfMonth)),
      db
        .select({ businessCount: count() })
        .from(customers)
        .where(eq(customers.legalType, "business")),
      db
        .select({ privateCount: count() })
        .from(customers)
        .where(eq(customers.legalType, "private")),
    ]);

  return { total, active, newThisMonth, businessCount, privateCount };
}
