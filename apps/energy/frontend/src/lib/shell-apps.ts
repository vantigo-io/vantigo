import { IconBolt, IconMessage, IconPackage, IconUsers } from "@tabler/icons-react";
import type { ShellApp } from "@vantigo/frontend-shell";

const productsUrl = import.meta.env.VITE_PRODUCTS_URL || "http://localhost:10013/products";
const customersUrl = import.meta.env.VITE_CUSTOMERS_URL || "http://localhost:10011/customers";
const communicationsUrl = import.meta.env.VITE_COMMUNICATIONS_URL || "http://localhost:10012/communications";

export const shellApps: readonly ShellApp[] = [
  { id: "energy", label: "Energy", icon: IconBolt, current: true },
  ...(customersUrl ? [{ id: "customers", label: "Customers", icon: IconUsers, url: customersUrl }] : []),
  ...(productsUrl ? [{ id: "products", label: "Products", icon: IconPackage, url: productsUrl }] : []),
  ...(communicationsUrl
    ? [{ id: "communications", label: "Communications", icon: IconMessage, url: communicationsUrl }]
    : []),
];
