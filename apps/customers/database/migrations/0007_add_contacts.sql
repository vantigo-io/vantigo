CREATE TABLE "customers"."contacts" (
	"id" integer PRIMARY KEY GENERATED ALWAYS AS IDENTITY (sequence name "customers"."contacts_id_seq" INCREMENT BY 1 MINVALUE 1 MAXVALUE 2147483647 START WITH 1 CACHE 1),
	"name" text NOT NULL,
	"email" text,
	"phone" text,
	"notes" text,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"updated_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "customers"."customers_contacts" (
	"customer_id" integer NOT NULL,
	"contact_id" integer NOT NULL,
	"role" text,
	"is_primary" boolean DEFAULT false NOT NULL,
	"created_at" timestamp DEFAULT now() NOT NULL,
	CONSTRAINT "customers_contacts_customer_id_contact_id_pk" PRIMARY KEY("customer_id","contact_id")
);
--> statement-breakpoint
ALTER TABLE "customers"."customers_contacts" ADD CONSTRAINT "customers_contacts_customer_id_customers_id_fk" FOREIGN KEY ("customer_id") REFERENCES "customers"."customers"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "customers"."customers_contacts" ADD CONSTRAINT "customers_contacts_contact_id_contacts_id_fk" FOREIGN KEY ("contact_id") REFERENCES "customers"."contacts"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "contacts_name_idx" ON "customers"."contacts" USING btree ("name");--> statement-breakpoint
CREATE INDEX "contacts_email_idx" ON "customers"."contacts" USING btree ("email");--> statement-breakpoint
CREATE UNIQUE INDEX "customers_contacts_primary_idx" ON "customers"."customers_contacts" USING btree ("customer_id") WHERE "customers"."customers_contacts"."is_primary";--> statement-breakpoint
CREATE INDEX "customers_contacts_contact_idx" ON "customers"."customers_contacts" USING btree ("contact_id");