CREATE TABLE "customers"."customers" (
	"id" integer PRIMARY KEY GENERATED ALWAYS AS IDENTITY (sequence name "customers"."customers_id_seq" INCREMENT BY 1 MINVALUE 1 MAXVALUE 2147483647 START WITH 1001 CACHE 1),
	"legal_name" text NOT NULL,
	"legal_type" text NOT NULL,
	"legal_country" text NOT NULL,
	"legal_id" text NOT NULL,
	"email" text NOT NULL,
	"phone" text NOT NULL,
	"status" text DEFAULT 'active' NOT NULL,
	"notes" text,
	"created_at" timestamp DEFAULT now() NOT NULL,
	"updated_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE INDEX "customers_legal_name_idx" ON "customers"."customers" USING btree ("legal_name");--> statement-breakpoint
CREATE INDEX "customers_status_idx" ON "customers"."customers" USING btree ("status");--> statement-breakpoint
CREATE INDEX "customers_legal_type_idx" ON "customers"."customers" USING btree ("legal_type");