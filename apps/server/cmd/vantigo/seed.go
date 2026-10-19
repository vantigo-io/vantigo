package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const (
	demoDefaultPassword     = "VantigoIsAwesome!"
	demoOwnerEmail          = "owner@vantigo.local"
	demoFirstCustomerNumber = int64(10001)
	demoCustomerCount       = 200
	demoContactsPerCustomer = 2
	demoProductCount        = 50
	demoProjectCount        = 75
	demoUserCount           = 20
)

type demoStaffSeed struct {
	email       string
	firstName   string
	lastName    string
	preferred   string
	jobTitle    string
	deptCode    string
	locationIdx int
	birthDate   string
	startDate   string
	billRate    float64
	costRate    float64
	annualPay   float64
	roleName    string
}

var demoStaffMembers = []demoStaffSeed{
	{email: demoOwnerEmail, firstName: "Astrid", lastName: "Lindqvist", preferred: "Astrid", jobTitle: "Managing Partner", deptCode: "LEAD", locationIdx: 0, birthDate: "1982-04-14", startDate: "2020-01-15", billRate: 2100, costRate: 950, annualPay: 1450000, roleName: "Owner"},
	{email: "henrik.solberg@vantigo.local", firstName: "Henrik", lastName: "Solberg", preferred: "Henrik", jobTitle: "Practice Director", deptCode: "LEAD", locationIdx: 0, birthDate: "1984-09-22", startDate: "2020-03-01", billRate: 1950, costRate: 890, annualPay: 1320000, roleName: "Practice Lead"},
	{email: "ingrid.nygaard@vantigo.local", firstName: "Ingrid", lastName: "Nygård", preferred: "Ingrid", jobTitle: "Principal Architect", deptCode: "ENG", locationIdx: 0, birthDate: "1986-11-08", startDate: "2020-08-15", billRate: 1850, costRate: 840, annualPay: 1240000, roleName: "Practice Lead"},
	{email: "magnus.bakken@vantigo.local", firstName: "Magnus", lastName: "Bakken", preferred: "Magnus", jobTitle: "Delivery Lead", deptCode: "CONS", locationIdx: 0, birthDate: "1985-02-19", startDate: "2021-01-10", billRate: 1750, costRate: 800, annualPay: 1180000, roleName: "Practice Lead"},
	{email: "sofie.haugen@vantigo.local", firstName: "Sofie", lastName: "Haugen", preferred: "Sofie", jobTitle: "Finance & Operations Manager", deptCode: "OPS", locationIdx: 0, birthDate: "1987-06-30", startDate: "2021-02-01", billRate: 1650, costRate: 760, annualPay: 1120000, roleName: "Finance & Billing"},
	{email: "erik.johansen@vantigo.local", firstName: "Erik", lastName: "Johansen", preferred: "Erik", jobTitle: "Regional Lead West", deptCode: "CONS", locationIdx: 1, birthDate: "1983-12-03", startDate: "2021-04-01", billRate: 1800, costRate: 820, annualPay: 1210000, roleName: "Practice Lead"},
	{email: "nora.berg@vantigo.local", firstName: "Nora", lastName: "Berg", preferred: "Nora", jobTitle: "Senior Cloud Consultant", deptCode: "ENG", locationIdx: 0, birthDate: "1990-03-17", startDate: "2021-08-15", billRate: 1650, costRate: 740, annualPay: 1050000, roleName: "Consultant"},
	{email: "jakob.dahl@vantigo.local", firstName: "Jakob", lastName: "Dahl", preferred: "Jakob", jobTitle: "Senior Billing Controller", deptCode: "OPS", locationIdx: 0, birthDate: "1989-07-25", startDate: "2021-09-01", billRate: 1700, costRate: 760, annualPay: 1080000, roleName: "Finance & Billing"},
	{email: "emilie.lund@vantigo.local", firstName: "Emilie", lastName: "Lund", preferred: "Emilie", jobTitle: "Lead Product Designer", deptCode: "ENG", locationIdx: 0, birthDate: "1991-01-12", startDate: "2022-01-15", billRate: 1600, costRate: 720, annualPay: 990000, roleName: "Consultant"},
	{email: "jonas.moen@vantigo.local", firstName: "Jonas", lastName: "Moen", preferred: "Jonas", jobTitle: "Senior Platform Engineer", deptCode: "ENG", locationIdx: 1, birthDate: "1988-05-04", startDate: "2022-03-01", billRate: 1650, costRate: 740, annualPay: 1040000, roleName: "Consultant"},
	{email: "thea.hagen@vantigo.local", firstName: "Thea", lastName: "Hagen", preferred: "Thea", jobTitle: "Energy Domain Specialist", deptCode: "CONS", locationIdx: 1, birthDate: "1992-10-19", startDate: "2022-05-01", billRate: 1600, costRate: 710, annualPay: 980000, roleName: "Customer Success"},
	{email: "sander.lie@vantigo.local", firstName: "Sander", lastName: "Lie", preferred: "Sander", jobTitle: "Senior Backend Engineer", deptCode: "ENG", locationIdx: 0, birthDate: "1991-08-28", startDate: "2022-08-15", billRate: 1600, costRate: 720, annualPay: 995000, roleName: "Consultant"},
	{email: "frida.eide@vantigo.local", firstName: "Frida", lastName: "Eide", preferred: "Frida", jobTitle: "Management Consultant", deptCode: "CONS", locationIdx: 0, birthDate: "1993-04-09", startDate: "2023-01-10", billRate: 1500, costRate: 680, annualPay: 920000, roleName: "Consultant"},
	{email: "tobias.strand@vantigo.local", firstName: "Tobias", lastName: "Strand", preferred: "Tobias", jobTitle: "HR & Compliance Lead", deptCode: "OPS", locationIdx: 0, birthDate: "1987-11-21", startDate: "2023-02-01", billRate: 1750, costRate: 790, annualPay: 1140000, roleName: "HR & People Manager"},
	{email: "maja.holm@vantigo.local", firstName: "Maja", lastName: "Holm", preferred: "Maja", jobTitle: "Customer Success Lead", deptCode: "OPS", locationIdx: 0, birthDate: "1994-02-14", startDate: "2023-04-01", billRate: 1450, costRate: 650, annualPay: 880000, roleName: "Customer Success"},
	{email: "oliver.aas@vantigo.local", firstName: "Oliver", lastName: "Aas", preferred: "Oliver", jobTitle: "Data & Analytics Engineer", deptCode: "ENG", locationIdx: 1, birthDate: "1993-09-07", startDate: "2023-08-15", billRate: 1550, costRate: 690, annualPay: 940000, roleName: "Consultant"},
	{email: "amalie.vik@vantigo.local", firstName: "Amalie", lastName: "Vik", preferred: "Amalie", jobTitle: "Project Consultant", deptCode: "CONS", locationIdx: 1, birthDate: "1995-06-16", startDate: "2024-01-08", billRate: 1400, costRate: 640, annualPay: 850000, roleName: "Consultant"},
	{email: "kasper.myhre@vantigo.local", firstName: "Kasper", lastName: "Myhre", preferred: "Kasper", jobTitle: "Fullstack Engineer", deptCode: "ENG", locationIdx: 0, birthDate: "1995-12-11", startDate: "2024-03-01", billRate: 1450, costRate: 660, annualPay: 890000, roleName: "Consultant"},
	{email: "hedda.bo@vantigo.local", firstName: "Hedda", lastName: "Bø", preferred: "Hedda", jobTitle: "People & Culture Partner", deptCode: "OPS", locationIdx: 0, birthDate: "1992-03-29", startDate: "2024-05-01", billRate: 1400, costRate: 650, annualPay: 870000, roleName: "HR & People Manager"},
	{email: "mathias.rask@vantigo.local", firstName: "Mathias", lastName: "Rask", preferred: "Mathias", jobTitle: "Internal Auditor", deptCode: "OPS", locationIdx: 0, birthDate: "1997-07-02", startDate: "2024-08-15", billRate: 1300, costRate: 600, annualPay: 780000, roleName: "Read-Only Auditor"},
}

type demoRoleSpec struct {
	id          uuid.UUID
	name        string
	description string
	permissions []string
}

var demoCustomRoles = []demoRoleSpec{
	{
		id:          uuid.MustParse("20000000-0000-4000-8000-000000000001"),
		name:        "Practice Lead",
		description: "Manages projects, approves time and expenses, views financials, and oversees delivery.",
		permissions: []string{
			"customers:view", "customers:create", "customers:update",
			"customers:legal-identity-view", "customers:contacts-view", "customers:contacts-manage",
			"customers:associations-view", "customers:associations-manage",
			"customers:timeline-view", "customers:timeline-manage", "customers:lookup-view",
			"products:products-view", "products:products-manage",
			"products:variants-view", "products:variants-manage",
			"products:pricing-view", "products:pricing-manage",
			"products:categories-view", "products:categories-manage",
			"products:tax-categories-view", "products:tax-categories-manage",
			"projects:access", "projects:create", "projects:view-all", "projects:manage-all",
			"projects:view-financials", "projects:view-costs",
			"time:access", "time:approve", "time:view-all", "time:manage",
			"expenses:access", "expenses:approve", "expenses:view-all",
			"invoices:access", "invoices:create",
			"staff:access", "staff:view", "staff:approve",
			"communications:conversations-view", "communications:conversations-reply", "communications:conversations-manage",
			"energy:metering-points-view", "energy:meters-view", "energy:supply-periods-view", "energy:consumption-view",
			"calendar:access", "calendar:view-colleagues",
		},
	},
	{
		id:          uuid.MustParse("20000000-0000-4000-8000-000000000002"),
		name:        "Finance & Billing",
		description: "Issues invoices, records payments, manages products/pricing, and audits time and expenses.",
		permissions: []string{
			"invoices:access", "invoices:create", "invoices:issue", "invoices:manage", "invoices:payments",
			"expenses:access", "expenses:approve", "expenses:view-all", "expenses:manage",
			"time:access", "time:view-all", "time:manage",
			"projects:access", "projects:view-all", "projects:view-financials", "projects:view-costs",
			"customers:view", "customers:create", "customers:update",
			"customers:legal-identity-view", "customers:legal-identity-manage",
			"customers:contacts-view", "customers:associations-view",
			"customers:timeline-view", "customers:lookup-view", "customers:billing-manage",
			"products:products-view", "products:products-manage",
			"products:variants-view", "products:variants-manage",
			"products:pricing-view", "products:pricing-manage",
			"products:categories-view", "products:categories-manage",
			"products:tax-categories-view", "products:tax-categories-manage",
			"staff:access", "staff:view",
			"calendar:access", "calendar:view-colleagues",
		},
	},
	{
		id:          uuid.MustParse("20000000-0000-4000-8000-000000000003"),
		name:        "HR & People Manager",
		description: "Manages staff directory, personal data, absence approvals, and employee compensation.",
		permissions: []string{
			"staff:access", "staff:view", "staff:approve", "staff:manage", "staff:personal-data",
			"customers:view", "customers:personal-data",
			"projects:access", "projects:view-all",
			"time:access", "time:view-all", "time:approve",
			"expenses:access", "expenses:view-all",
			"calendar:access", "calendar:view-colleagues",
		},
	},
	{
		id:          uuid.MustParse("20000000-0000-4000-8000-000000000004"),
		name:        "Customer Success",
		description: "Manages CRM companies, contacts, communications inbox, and energy metering points.",
		permissions: []string{
			"customers:view", "customers:create", "customers:update", "customers:delete",
			"customers:legal-identity-view", "customers:legal-identity-manage",
			"customers:contacts-view", "customers:contacts-manage",
			"customers:associations-view", "customers:associations-manage",
			"customers:timeline-view", "customers:timeline-manage",
			"customers:lookup-view", "customers:billing-manage", "customers:merge", "customers:import",
			"communications:channels-manage", "communications:conversations-manage",
			"communications:conversations-reply", "communications:conversations-view",
			"communications:suppressions-manage",
			"energy:consumption-manage", "energy:consumption-view",
			"energy:metering-points-manage", "energy:metering-points-view",
			"energy:meters-manage", "energy:meters-view",
			"energy:supply-periods-manage", "energy:supply-periods-view", "energy:import",
			"products:products-view", "products:variants-view", "products:pricing-view",
			"projects:access", "projects:view-all",
			"invoices:access",
			"time:access", "expenses:access", "staff:access", "staff:view",
			"calendar:access",
		},
	},
	{
		id:          uuid.MustParse("20000000-0000-4000-8000-000000000005"),
		name:        "Consultant",
		description: "Logs time and expenses, works on assigned projects, and views customer and product records.",
		permissions: []string{
			"projects:access",
			"time:access",
			"expenses:access",
			"staff:access", "staff:view",
			"customers:view", "customers:contacts-view", "customers:associations-view",
			"customers:timeline-view", "customers:timeline-manage",
			"products:products-view", "products:variants-view", "products:pricing-view",
			"products:categories-view", "products:tax-categories-view",
			"energy:metering-points-view", "energy:meters-view",
			"energy:supply-periods-view", "energy:consumption-view",
			"calendar:access",
		},
	},
	{
		id:          uuid.MustParse("20000000-0000-4000-8000-000000000006"),
		name:        "Read-Only Auditor",
		description: "Read-only access across all operational modules without write or approval permissions.",
		permissions: []string{
			"customers:view", "customers:legal-identity-view", "customers:contacts-view",
			"customers:associations-view", "customers:timeline-view",
			"products:products-view", "products:variants-view", "products:pricing-view",
			"products:categories-view", "products:tax-categories-view",
			"projects:access", "projects:view-all", "projects:view-financials", "projects:view-costs",
			"time:access", "time:view-all",
			"expenses:access", "expenses:view-all",
			"invoices:access",
			"staff:access", "staff:view",
			"energy:metering-points-view", "energy:meters-view",
			"energy:supply-periods-view", "energy:consumption-view",
			"communications:conversations-view",
			"calendar:access",
		},
	},
}

func seed(ctx context.Context, logger *slog.Logger, cfg *config.Config) int {
	if code := migrate(logger, cfg); code != 0 {
		return code
	}

	pool, err := db.Open(context.WithoutCancel(ctx), cfg.DatabaseURL)
	if err != nil {
		logger.Error("seed failed to open database", "error", err)
		return 1
	}
	defer func() { closePool(pool, cfg.ShutdownTimeout, logger) }()

	if err := seedDemoDataset(ctx, pool, logger); err != nil {
		logger.Error("development seed failed", "error", err)
		return 1
	}
	return 0
}

func seedDemoDataset(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	now := time.Now().UTC().Truncate(time.Second)
	pwHash, err := seedPasswordHash(demoDefaultPassword)
	if err != nil {
		return err
	}

	var skipped bool
	var createdDefaultOwner bool
	err = db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var alreadySeeded bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM customers.customers WHERE customer_number >= $1)`,
			demoFirstCustomerNumber,
		).Scan(&alreadySeeded); err != nil {
			return fmt.Errorf("check existing seed state: %w", err)
		}
		if alreadySeeded {
			if err := backfillDemoAvatarsIfMissing(ctx, tx, now); err != nil {
				return fmt.Errorf("backfill demo avatars: %w", err)
			}
			if err := backfillDemoCalendar(ctx, tx, now); err != nil {
				return fmt.Errorf("backfill demo calendar: %w", err)
			}
			skipped = true
			return nil
		}

		userIDs, ownerCreated, err := seedIdentityAndStaff(ctx, tx, now, pwHash)
		if err != nil {
			return fmt.Errorf("seed identity and staff: %w", err)
		}
		createdDefaultOwner = ownerCreated

		customerIDs, contactIDs, err := seedCustomersAndContacts(ctx, tx, now, userIDs)
		if err != nil {
			return fmt.Errorf("seed customers and contacts: %w", err)
		}

		variantIDs, err := seedProductsCatalog(ctx, tx, now)
		if err != nil {
			return fmt.Errorf("seed products catalog: %w", err)
		}

		projectIDs, billingLineIDs, err := seedProjectsPortfolio(ctx, tx, now, userIDs, customerIDs, variantIDs)
		if err != nil {
			return fmt.Errorf("seed projects portfolio: %w", err)
		}

		if err := seedTimeAndExpenses(ctx, tx, now, userIDs, projectIDs, billingLineIDs); err != nil {
			return fmt.Errorf("seed time and expenses: %w", err)
		}

		if err := seedInvoicesReceivables(ctx, tx, now, userIDs, customerIDs, projectIDs); err != nil {
			return fmt.Errorf("seed invoices: %w", err)
		}

		if err := seedEnergyAndCommunications(ctx, tx, now, userIDs, customerIDs, contactIDs); err != nil {
			return fmt.Errorf("seed energy and communications: %w", err)
		}

		if err := seedCalendar(ctx, tx, now, userIDs, customerIDs); err != nil {
			return fmt.Errorf("seed calendar: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	if skipped {
		logger.Info("development seed already applied; skipping")
		return nil
	}

	attrs := []any{
		"users", demoUserCount,
		"companies", demoCustomerCount,
		"contacts", demoCustomerCount * demoContactsPerCustomer,
		"products", demoProductCount,
		"projects", demoProjectCount,
	}
	if createdDefaultOwner {
		attrs = append(attrs, "owner_email", demoOwnerEmail, "default_password", demoDefaultPassword)
	}
	logger.Info("development demo seed applied", attrs...)
	return nil
}

func seedPasswordHash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("seed password salt: %w", err)
	}
	mem := uint32(19456)
	passes := uint32(2)
	if testing.Testing() {
		mem = 8
		passes = 1
	}
	key := argon2.IDKey([]byte(password), salt, passes, mem, 1, 32)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=1$%s$%s",
		argon2.Version, mem, passes,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func seedIdentityAndStaff(ctx context.Context, tx pgx.Tx, now time.Time, pwHash string) ([]uuid.UUID, bool, error) {
	userIDs := make([]uuid.UUID, len(demoStaffMembers))
	createdOwner := false

	// Check if an Owner user already exists on this installation.
	var existingOwnerID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT u.id
		FROM identity.users u
		JOIN identity.user_roles ur ON ur.user_id = u.id
		JOIN identity.roles r ON r.id = ur.role_id
		WHERE r.name = 'Owner'
		ORDER BY u.created_at
		LIMIT 1
	`).Scan(&existingOwnerID)
	switch {
	case err == nil:
		userIDs[0] = existingOwnerID
	case errors.Is(err, pgx.ErrNoRows):
		ownerID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
		m := demoStaffMembers[0]
		displayName := m.firstName + " " + m.lastName
		if err := tx.QueryRow(ctx, `
			INSERT INTO identity.users (
				id, email, normalized_email, email_confirmed, display_name,
				password_hash, version, created_at, updated_at
			) VALUES ($1, $2, $3, true, $4, $5, $6, $7, $7)
			ON CONFLICT (normalized_email) DO UPDATE SET updated_at = EXCLUDED.updated_at
			RETURNING id
		`, ownerID, m.email, strings.ToUpper(m.email), displayName, pwHash, uuid.New(), now).Scan(&ownerID); err != nil {
			return nil, false, err
		}
		for _, roleID := range []uuid.UUID{identity.RoleOwnerID, identity.RoleSystemAdminID, identity.RoleUserID} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO identity.user_roles (user_id, role_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, ownerID, roleID); err != nil {
				return nil, false, err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO identity.bootstrap_state (id, completed_at)
			VALUES (1, $1)
			ON CONFLICT (id) DO NOTHING
		`, now); err != nil {
			return nil, false, err
		}
		userIDs[0] = ownerID
		createdOwner = true
	default:
		return nil, false, err
	}

	// Create the 6 custom roles so testers can log in as different personas to test permission boundaries.
	roleIDByName := make(map[string]uuid.UUID, len(demoCustomRoles))
	for _, r := range demoCustomRoles {
		roleID := r.id
		if err := tx.QueryRow(ctx, `
			INSERT INTO identity.roles (
				id, name, normalized_name, display_name, description,
				is_system, is_built_in, version, created_at, updated_at
			)
			VALUES ($1, $2, $3, $2, $4, false, false, $5, $6, $6)
			ON CONFLICT (normalized_name) DO UPDATE SET name = EXCLUDED.name, updated_at = EXCLUDED.updated_at
			RETURNING id
		`, roleID, r.name, strings.ToUpper(r.name), r.description, uuid.New(), now).Scan(&roleID); err != nil {
			return nil, false, err
		}
		roleIDByName[r.name] = roleID

		for _, permKey := range r.permissions {
			if _, err := tx.Exec(ctx, `
				INSERT INTO identity.role_permissions (role_id, permission_key)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, roleID, permKey); err != nil {
				return nil, false, err
			}
		}
	}

	for i := 1; i < len(demoStaffMembers); i++ {
		m := demoStaffMembers[i]
		uid := uuid.MustParse(fmt.Sprintf("10000000-0000-4000-8000-%012d", i+1))
		displayName := m.firstName + " " + m.lastName
		if err := tx.QueryRow(ctx, `
			INSERT INTO identity.users (
				id, email, normalized_email, email_confirmed, display_name,
				password_hash, version, created_at, updated_at
			) VALUES ($1, $2, $3, true, $4, $5, $6, $7, $7)
			ON CONFLICT (normalized_email) DO UPDATE SET updated_at = EXCLUDED.updated_at
			RETURNING id
		`, uid, m.email, strings.ToUpper(m.email), displayName, pwHash, uuid.New(), now).Scan(&uid); err != nil {
			return nil, false, err
		}
		userIDs[i] = uid

		customRoleID, ok := roleIDByName[m.roleName]
		if !ok {
			return nil, false, fmt.Errorf("unknown demo role %q", m.roleName)
		}
		for _, rid := range []uuid.UUID{identity.RoleUserID, customRoleID} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO identity.user_roles (user_id, role_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, uid, rid); err != nil {
				return nil, false, err
			}
		}
	}

	if err := seedDemoAvatars(ctx, tx, now, userIDs); err != nil {
		return nil, false, err
	}

	// Seed Staff organisation structure and 20 employee records.
	if _, err := tx.Exec(ctx, `
		UPDATE staff.settings
		SET employee_number_prefix = 'EMP-',
		    next_employee_number = $1,
		    updated_at = $2
		WHERE id = 1
	`, len(demoStaffMembers)+1, now); err != nil {
		return nil, false, err
	}

	// Ensure a default legal entity and business unit exist.
	if _, err := tx.Exec(ctx, `UPDATE staff.legal_entities SET is_default = false WHERE is_default`); err != nil {
		return nil, false, err
	}
	var legalEntityID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO staff.legal_entities (
			name, org_number, is_default, created_at, updated_at
		) VALUES ('Vantigo Consulting AS', '987654325', true, $1, $1)
		ON CONFLICT (org_number)
		DO UPDATE SET is_default = true, updated_at = EXCLUDED.updated_at
		RETURNING id
	`, now).Scan(&legalEntityID); err != nil {
		return nil, false, err
	}

	if _, err := tx.Exec(ctx, `UPDATE staff.business_units SET is_default = false WHERE is_default`); err != nil {
		return nil, false, err
	}
	var osloBUID, bergenBUID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO staff.business_units (
			legal_entity_id, name, org_number, is_default, created_at, updated_at
		) VALUES ($1, 'Vantigo Oslo HQ', '976543215', true, $2, $2)
		ON CONFLICT (org_number)
		DO UPDATE SET is_default = true, updated_at = EXCLUDED.updated_at
		RETURNING id
	`, legalEntityID, now).Scan(&osloBUID); err != nil {
		return nil, false, err
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO staff.business_units (
			legal_entity_id, name, org_number, is_default, created_at, updated_at
		) VALUES ($1, 'Vantigo Vestlandet', '965432104', false, $2, $2)
		ON CONFLICT (org_number)
		DO UPDATE SET updated_at = EXCLUDED.updated_at
		RETURNING id
	`, legalEntityID, now).Scan(&bergenBUID); err != nil {
		return nil, false, err
	}

	deptIDs := make(map[string]int64, 4)
	for _, d := range []struct {
		code string
		name string
	}{
		{"LEAD", "Executive & Leadership"},
		{"CONS", "Advisory & Delivery"},
		{"ENG", "Product & Engineering"},
		{"OPS", "Finance & Operations"},
	} {
		var deptID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO staff.departments (code, name, created_at, updated_at)
			VALUES ($1, $2, $3, $3)
			ON CONFLICT (upper(code)) DO UPDATE SET name = EXCLUDED.name, updated_at = EXCLUDED.updated_at
			RETURNING id
		`, d.code, d.name, now).Scan(&deptID); err != nil {
			return nil, false, err
		}
		deptIDs[d.code] = deptID
	}

	locationIDs := make([]int64, 2)
	for idx, loc := range []struct {
		name       string
		street     string
		postalCode string
		city       string
	}{
		{"Oslo HQ", "Karl Johans gate 41", "0162", "Oslo"},
		{"Bergen Studio", "Bryggen 11", "5003", "Bergen"},
	} {
		if err := tx.QueryRow(ctx, `
			INSERT INTO staff.locations (name, street, postal_code, city, country, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'NO', $5, $5)
			RETURNING id
		`, loc.name, loc.street, loc.postalCode, loc.city, now).Scan(&locationIDs[idx]); err != nil {
			return nil, false, err
		}
	}

	var holidaySchemeID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM staff.holiday_schemes WHERE is_default LIMIT 1`).Scan(&holidaySchemeID); err != nil {
		return nil, false, err
	}

	personIDs := make([]int64, len(demoStaffMembers))
	for i, m := range demoStaffMembers {
		empNumber := fmt.Sprintf("EMP-%03d", i+1)
		workPhone := fmt.Sprintf("+47 90%02d %02d %02d", 10+i, 20+i, 30+i)
		bankAcct := norwegianBankAccount(fmt.Sprintf("150610%04d", i+1))
		bankLast4 := bankAcct[len(bankAcct)-4:]
		displayName := m.firstName + " " + m.lastName
		var managerPersonID *int64
		if i > 0 {
			mgr := personIDs[0]
			if i >= 6 {
				mgr = personIDs[1+(i%5)]
			}
			managerPersonID = &mgr
		}

		var personID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO staff.people (
				employee_number, given_name, family_name, display_name, birth_date,
				work_email, phone_work, street, postal_code, city, country,
				work_country, preferred_language, bank_account_last4, bank_account_kind,
				created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5::date,
				$6, $7, $8, $9, $10, 'NO',
				'NO', 'nb', $11, 'bban',
				$12, $12
			)
			ON CONFLICT (upper(employee_number COLLATE "C"))
			DO UPDATE SET updated_at = EXCLUDED.updated_at
			RETURNING id
		`,
			empNumber, m.firstName, m.lastName, displayName, m.birthDate,
			m.email, workPhone, fmt.Sprintf("Parkveien %d", 10+i), "0258", "Oslo",
			bankLast4, now,
		).Scan(&personID); err != nil {
			return nil, false, err
		}
		personIDs[i] = personID

		if _, err := tx.Exec(ctx, `
			INSERT INTO staff.next_of_kin (person_id, position, name, relationship, phone, created_at, updated_at)
			VALUES ($1, 1, $2, 'spouse', $3, $4, $4)
		`, personID, fmt.Sprintf("Kontakt %s", m.lastName), fmt.Sprintf("+47 41%02d %02d %02d", 10+i, 20+i, 30+i), now); err != nil {
			return nil, false, err
		}

		buID := osloBUID
		if m.locationIdx == 1 {
			buID = bergenBUID
		}
		var empID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO staff.employments (
				person_id, employment_number, relationship_type, business_unit_id,
				start_date, contract_signed_at, holiday_scheme_id, created_at, updated_at
			) VALUES ($1, $2, 'ordinary', $3, $4::date, $4::date, $5, $6, $6)
			ON CONFLICT (upper(employment_number))
			DO UPDATE SET updated_at = EXCLUDED.updated_at
			RETURNING id
		`, personID, fmt.Sprintf("ARB-%03d", i+1), buID, m.startDate, holidaySchemeID, now).Scan(&empID); err != nil {
			return nil, false, err
		}

		payJSON := fmt.Sprintf(`[{"name":"Fastlønn","amount":%.2f,"currency":"NOK","period":"yearly"}]`, m.annualPay)
		if _, err := tx.Exec(ctx, `
			INSERT INTO staff.employment_terms (
				employment_id, valid_from, employment_type, appointment_form,
				fte_percent, working_hours_scheme, full_time_hours_per_week,
				occupation_code, title, employee_type, remuneration_type, pay_components,
				department_id, location_id, manager_person_id,
				created_at, updated_at
			) VALUES (
				$1, $2::date, 'fast', 'fast',
				100.00, 'not_shift', 37.50,
				'2421104', $3, 'employee', 'monthly', $4::jsonb,
				$5, $6, $7,
				$8, $8
			)
			ON CONFLICT (employment_id, valid_from) DO NOTHING
		`, empID, m.startDate, m.jobTitle, payJSON, deptIDs[m.deptCode], locationIDs[m.locationIdx], managerPersonID, now); err != nil {
			return nil, false, err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO staff.identity_links (
				person_id, provider, external_id, state, managed, created_at, updated_at
			) VALUES ($1, 'identity', $2, 'linked', false, $3, $3)
			ON CONFLICT (provider, external_id) DO NOTHING
		`, personID, userIDs[i].String(), now); err != nil {
			return nil, false, err
		}
	}

	// Seed a handful of approved and requested absences so the Staff Absence & Inbox views are populated.
	var ferieTypeID, egenmeldingTypeID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM staff.absence_types WHERE key = 'ferie'`).Scan(&ferieTypeID); err != nil {
		return nil, false, err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM staff.absence_types WHERE key = 'egenmelding'`).Scan(&egenmeldingTypeID); err != nil {
		return nil, false, err
	}

	for i := 1; i <= 6; i++ {
		start := now.AddDate(0, 0, 7*i)
		end := start.AddDate(0, 0, 4)
		status := "approved"
		decidedBy := &userIDs[0]
		decidedAt := &now
		if i >= 5 {
			status = "requested"
			decidedBy = nil
			decidedAt = nil
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO staff.absences (
				person_id, type_id, first_day, last_day, percent, status, note,
				requested_by_user_id, decided_by_user_id, decided_at, created_at, updated_at
			) VALUES ($1, $2, $3::date, $4::date, 100, $5, 'Planlagt ferie', $6, $7, $8, $9, $9)
		`, personIDs[i], ferieTypeID, start.Format("2006-01-02"), end.Format("2006-01-02"), status, userIDs[i], decidedBy, decidedAt, now); err != nil {
			return nil, false, err
		}
	}

	var sickCaseID int64
	sickStart := now.AddDate(0, 0, -12).Format("2006-01-02")
	sickEnd := now.AddDate(0, 0, -10).Format("2006-01-02")
	if err := tx.QueryRow(ctx, `
		INSERT INTO staff.sick_cases (person_id, first_day, last_day, closed_at, created_at, updated_at)
		VALUES ($1, $2::date, $3::date, $4, $4, $4)
		RETURNING id
	`, personIDs[7], sickStart, sickEnd, now).Scan(&sickCaseID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO staff.absences (
			person_id, type_id, first_day, last_day, percent, status,
			case_id, degree_percent, requested_by_user_id, created_at, updated_at
		) VALUES ($1, $2, $3::date, $4::date, 100, 'registered', $5, 100, $6, $7, $7)
	`, personIDs[7], egenmeldingTypeID, sickStart, sickEnd, sickCaseID, userIDs[7], now); err != nil {
		return nil, false, err
	}

	return userIDs, createdOwner, nil
}

func seedCustomersAndContacts(ctx context.Context, tx pgx.Tx, now time.Time, userIDs []uuid.UUID) ([]int32, []int32, error) {
	groups := []struct {
		id   uuid.UUID
		name string
		days int32
	}{
		{uuid.MustParse("30000000-0000-4000-8000-000000000001"), "Enterprise", 30},
		{uuid.MustParse("30000000-0000-4000-8000-000000000002"), "Public Sector", 30},
		{uuid.MustParse("30000000-0000-4000-8000-000000000003"), "Nordic Mid-Market", 14},
		{uuid.MustParse("30000000-0000-4000-8000-000000000004"), "Energy & Maritime", 21},
		{uuid.MustParse("30000000-0000-4000-8000-000000000005"), "Growth & Scaleups", 14},
	}
	for i := range groups {
		if err := tx.QueryRow(ctx, `
			INSERT INTO customers.customer_groups (id, name, default_payment_terms_days, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $4)
			ON CONFLICT (lower(name)) DO UPDATE SET updated_at = EXCLUDED.updated_at
			RETURNING id
		`, groups[i].id, groups[i].name, groups[i].days, now).Scan(&groups[i].id); err != nil {
			return nil, nil, err
		}
	}

	tags := []struct {
		id    uuid.UUID
		name  string
		color string
	}{
		{uuid.MustParse("31000000-0000-4000-8000-000000000001"), "VIP", "violet"},
		{uuid.MustParse("31000000-0000-4000-8000-000000000002"), "EHF Ready", "teal"},
		{uuid.MustParse("31000000-0000-4000-8000-000000000003"), "Strategic", "blue"},
		{uuid.MustParse("31000000-0000-4000-8000-000000000004"), "SLA Gold", "orange"},
		{uuid.MustParse("31000000-0000-4000-8000-000000000005"), "Retainer", "green"},
		{uuid.MustParse("31000000-0000-4000-8000-000000000006"), "Renewal Due", "red"},
	}
	for i := range tags {
		if err := tx.QueryRow(ctx, `
			INSERT INTO customers.tags (id, name, color)
			VALUES ($1, $2, $3)
			ON CONFLICT (lower(name)) DO UPDATE SET color = EXCLUDED.color
			RETURNING id
		`, tags[i].id, tags[i].name, tags[i].color).Scan(&tags[i].id); err != nil {
			return nil, nil, err
		}
	}

	prefixes := []string{
		"Nordic", "Fjord", "Skagerrak", "Viken", "Vestland", "Barents", "Dovre", "Grieg", "Fram", "Polaris",
		"Solstrand", "Kyst", "Lyse", "Aker", "Birkeland", "Norrøna", "Svalbard", "Hardanger", "Lofoten", "Rondane",
	}
	domains := []string{
		"Energi", "Maritim", "Kraft", "Teknologi", "Logistikk", "Industri", "Finans", "Bygg", "Helse", "Digital",
	}
	cities := []struct {
		city       string
		postalCode string
		street     string
	}{
		{"Oslo", "0150", "Dronningens gate"},
		{"Bergen", "5013", "Torgallmenningen"},
		{"Trondheim", "7011", "Munkegata"},
		{"Stavanger", "4006", "Kirkegata"},
		{"Tromsø", "9008", "Storgata"},
		{"Kristiansand", "4611", "Markens gate"},
		{"Drammen", "3015", "Bragernes torg"},
		{"Ålesund", "6002", "Kongens gate"},
	}
	firstNames := []string{
		"Kari", "Ola", "Silje", "Håkon", "Marte", "Sindre", "Camilla", "Espen", "Ragnhild", "Vegard",
		"Linn", "Morten", "Tonje", "Kristian", "Ida", "Stian", "Ingrid", "Andreas", "Julie", "Fredrik",
	}
	lastNames := []string{
		"Hansen", "Olsen", "Larsen", "Andersen", "Pedersen", "Nilsen", "Kristiansen", "Jensen", "Karlsen", "Johnsen",
		"Pettersen", "Eriksen", "Berg", "Haugen", "Hagen", "Johannessen", "Andreassen", "Jacobsen", "Dahl", "Jørgensen",
	}
	contactTitles := [2]string{"Daglig leder (CEO)", "Økonomisjef (CFO)"}

	customerIDs := make([]int32, demoCustomerCount)
	contactIDs := make([]int32, 0, demoCustomerCount*demoContactsPerCustomer)

	for i := 0; i < demoCustomerCount; i++ {
		custNum := demoFirstCustomerNumber + int64(i)
		prefix := prefixes[i%len(prefixes)]
		domain := domains[(i/len(prefixes))%len(domains)]
		friendlyName := fmt.Sprintf("%s %s", prefix, domain)
		if i >= len(prefixes)*len(domains) {
			friendlyName = fmt.Sprintf("%s %s %d", prefix, domain, i+1)
		}
		legalName := friendlyName + " AS"
		slug := slugifyDomain(prefix, domain, i)
		orgNum := norwegianOrgNumber(i + 1)
		cityInfo := cities[i%len(cities)]
		group := groups[i%len(groups)]
		ownerID := userIDs[i%len(userIDs)]

		status := "active"
		switch {
		case i >= 194:
			status = "archived"
		case i >= 188:
			status = "disabled"
		}

		delivery := "ehf"
		if i%3 == 0 {
			delivery = "email"
		}
		peppolID := "0192:" + orgNum
		billRate := float64(1450 + (i%6)*100)
		createdAt := now.AddDate(0, 0, -(demoCustomerCount - i))

		var custID int32
		if err := tx.QueryRow(ctx, `
			INSERT INTO customers.customers (
				customer_number, name, type, status,
				legal_country, legal_type, legal_id, legal_name, legal_source,
				email, phone, website,
				invoice_email, reminder_email, payment_terms_days, currency, language,
				invoice_delivery, reminder_delivery, peppol_id, buyer_reference,
				owner_user_id, group_id, default_bill_rate,
				revision, created_at, updated_at
			) VALUES (
				$1, $2, 'business', $3,
				'no', 'business', $4, $5, 'brreg',
				$6, $7, $8,
				$9, $9, $10, 'NOK', 'nb',
				$11, 'email', $12, $13,
				$14, $15, $16,
				1, $17, $17
			)
			RETURNING id
		`,
			custNum, friendlyName, status,
			orgNum, legalName,
			"post@"+slug, fmt.Sprintf("+47 22 %02d %02d %02d", 10+(i%80), 10+((i*3)%80), 10+((i*7)%80)), "https://"+slug,
			"faktura@"+slug, group.days,
			delivery, peppolID, fmt.Sprintf("REF-%d", 1000+i),
			ownerID, group.id, billRate,
			createdAt,
		).Scan(&custID); err != nil {
			return nil, nil, err
		}
		customerIDs[i] = custID

		// Primary invoice address for each company.
		if _, err := tx.Exec(ctx, `
			INSERT INTO customers.customer_addresses (
				customer_id, type, label, line1, postal_code, city, country, is_primary, created_at, updated_at
			) VALUES ($1, 'invoice', 'Hovedkontor', $2, $3, $4, 'no', true, $5, $5)
		`, custID, fmt.Sprintf("%s %d", cityInfo.street, 1+(i%60)), cityInfo.postalCode, cityInfo.city, createdAt); err != nil {
			return nil, nil, err
		}

		// Attach 1-2 tags per company.
		if _, err := tx.Exec(ctx, `
			INSERT INTO customers.customer_tags (customer_id, tag_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, custID, tags[i%len(tags)].id); err != nil {
			return nil, nil, err
		}
		if i%3 == 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO customers.customer_tags (customer_id, tag_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, custID, tags[(i+2)%len(tags)].id); err != nil {
				return nil, nil, err
			}
		}

		// Insert 2 contacts per company (400 contacts total).
		for cIdx := 0; cIdx < demoContactsPerCustomer; cIdx++ {
			fn := firstNames[(i*2+cIdx)%len(firstNames)]
			ln := lastNames[(i*3+cIdx*5)%len(lastNames)]
			email := fmt.Sprintf("%s.%s@%s", asciiSlug(fn), asciiSlug(ln), slug)
			phone := fmt.Sprintf("+47 91 %02d %02d %02d", 10+((i+cIdx)%80), 10+((i*5+cIdx)%80), 10+((i*9+cIdx)%80))

			var contactID int32
			if err := tx.QueryRow(ctx, `
				INSERT INTO customers.contacts (first_name, last_name, email, phone, created_at)
				VALUES ($1, $2, $3, $4, $5)
				RETURNING id
			`, fn, ln, email, phone, createdAt).Scan(&contactID); err != nil {
				return nil, nil, err
			}
			contactIDs = append(contactIDs, contactID)

			if _, err := tx.Exec(ctx, `
				INSERT INTO customers.customers_contacts (customer_id, contact_id, title, email, phone)
				VALUES ($1, $2, $3, $4, $5)
			`, custID, contactID, contactTitles[cIdx], email, phone); err != nil {
				return nil, nil, err
			}

			role := "decision_maker"
			if cIdx == 1 {
				role = "billing"
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO customers.customer_contact_roles (customer_id, contact_id, role, is_primary, created_at)
				VALUES ($1, $2, $3, true, $4)
			`, custID, contactID, role, createdAt); err != nil {
				return nil, nil, err
			}
		}
	}

	// Update customer-number counter past the 200 seeded companies.
	if _, err := tx.Exec(ctx, `
		INSERT INTO customers.counters (counter_name, next_value)
		VALUES ('customer-number', $1)
		ON CONFLICT (counter_name) DO UPDATE SET next_value = GREATEST(customers.counters.next_value, EXCLUDED.next_value)
	`, demoFirstCustomerNumber+demoCustomerCount); err != nil {
		return nil, nil, err
	}

	// Seed 30 manual timeline entries (with open/overdue follow-ups on the first 12) so the dashboard & customer timelines shine.
	eventTypes := []string{"note", "call", "meeting", "email"}
	summaries := []string{
		"Kvartalsvis gjennomgang av leveranse og veikart",
		"Oppfølging av rammeavtale og prisjustering",
		"Avklaring av ressursbehov for neste fase",
		"Statusmøte med styringsgruppen",
		"Gjennomgang av SLA-rapportering og kapasitet",
	}
	for i := 0; i < 30; i++ {
		custID := customerIDs[i]
		actorIdx := i % len(demoStaffMembers)
		actorID := userIDs[actorIdx]
		actorDisplay := demoStaffMembers[actorIdx].firstName + " " + demoStaffMembers[actorIdx].lastName
		occurredAt := now.AddDate(0, 0, -(30 - i))
		occurredOn := occurredAt.Format("2006-01-02")
		evType := eventTypes[i%len(eventTypes)]
		summary := summaries[i%len(summaries)]
		note := "Kunden er svært fornøyd med fremdriften i nåværende leveranse og ønsker tilbud på utvidet bistand i neste kvartal."

		var followUpOn *string
		var followUpAssignee *uuid.UUID
		if i < 12 {
			// Mix of overdue (-3..-1 days), due today (0), and upcoming (+1..+8 days).
			due := now.AddDate(0, 0, i-4).Format("2006-01-02")
			followUpOn = &due
			followUpAssignee = &actorID
		}

		if _, err := tx.Exec(ctx, `
			WITH entry AS (
				INSERT INTO customers.customers_timeline_entries (
					customer_id, provenance, producer, event_type, occurred_on, occurred_at,
					summary, note, payload_version, current_revision, state,
					actor_kind, actor_display, actor_user_id, created_at, updated_at,
					follow_up_on, follow_up_assignee_user_id
				) VALUES (
					$1, 'manual', 'customers.api', $2, $3::date, $4,
					$5, $6, 1, 1, 'active',
					'user', $7, $8, $4, $4,
					$9::date, $10
				)
				RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at,
				          summary, note, source_url, payload_json, payload_version, current_revision,
				          state, actor_kind, actor_display, actor_user_id, created_at, updated_at,
				          follow_up_on, follow_up_assignee_user_id, follow_up_done_at
			)
			INSERT INTO customers.customers_timeline_entries_revisions (
				customer_timeline_entry_id, revision_number, customer_id, provenance, producer, event_type,
				occurred_on, occurred_at, summary, note, source_url, payload_json, payload_version, current_revision,
				state, actor_kind, actor_display, actor_user_id, created_at, updated_at,
				follow_up_on, follow_up_assignee_user_id, follow_up_done_at
			)
			SELECT id, 1, customer_id, provenance, producer, event_type,
			       occurred_on, occurred_at, summary, note, source_url, payload_json, payload_version, current_revision,
			       state, actor_kind, actor_display, actor_user_id, created_at, updated_at,
			       follow_up_on, follow_up_assignee_user_id, follow_up_done_at
			FROM entry
		`, custID, evType, occurredOn, occurredAt, summary, note, actorDisplay, actorID, followUpOn, followUpAssignee); err != nil {
			return nil, nil, err
		}
	}

	return customerIDs, contactIDs, nil
}

func seedProductsCatalog(ctx context.Context, tx pgx.Tx, now time.Time) ([]int32, error) {
	var vat25ID, vat0ID int32
	if err := tx.QueryRow(ctx, `
		INSERT INTO products.tax_categories (name, kind, rate, created_at, updated_at)
		VALUES ('Standard MVA 25 %', 'vat', 0.2500, $1, $1)
		ON CONFLICT (name) DO UPDATE SET rate = EXCLUDED.rate, updated_at = EXCLUDED.updated_at
		RETURNING id
	`, now).Scan(&vat25ID); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO products.tax_categories (name, kind, rate, created_at, updated_at)
		VALUES ('Fritak / Eksport 0 %', 'vat', 0.0000, $1, $1)
		ON CONFLICT (name) DO UPDATE SET rate = EXCLUDED.rate, updated_at = EXCLUDED.updated_at
		RETURNING id
	`, now).Scan(&vat0ID); err != nil {
		return nil, err
	}

	catNames := []string{
		"Rådgivning & Konsulenttjenester",
		"Programvare & Lisenser",
		"Sky & Infrastruktur",
		"Kurs & Workshops",
		"Support & Beredskap (SLA)",
	}
	catIDs := make([]int32, len(catNames))
	for i, name := range catNames {
		if err := tx.QueryRow(ctx, `
			INSERT INTO products.product_categories (name)
			VALUES ($1)
			ON CONFLICT (parent_id, name) DO UPDATE SET name = EXCLUDED.name
			RETURNING id
		`, name).Scan(&catIDs[i]); err != nil {
			return nil, err
		}
	}

	productNames := []string{
		"Senior løsningsarkitekt", "Principal rådgiver", "Skyarkitekt (Azure / GCP)", "Senior backend-utvikler", "Senior frontend-utvikler",
		"Prosjektleder / Leveranseleder", "Sikkerhetsarkitekt & Pentest", "Data & plattform-ingeniør", "UX / Tjenestedesigner", "AI & maskinlæringsrådgiver",
		"Vantigo Core Plattformlisens", "Vantigo Enterprise SSO-modul", "Vantigo EHF & Aksesspunkt-pakke", "Vantigo Analyse & BI-tillegg", "Vantigo API Høyvolum-pakke",
		"Vantigo Energimåling-modul", "Vantigo Prosjektøkonomi Pro", "Vantigo HR & A-melding-modul", "Vantigo Sandkasse-miljø", "Vantigo Revisjon & Arkiv-pakke",
		"Administrert Kubernetes-kluster", "24/7 Overvåking & Hendelseshåndtering", "Dedikert PostgreSQL HA-database", "Sikkerhetskopiering & Katastrofegjenoppretting", "Nettverk & Nulltillit-sikring",
		"DDoS-beskyttelse & WAF", "Logginnsamling & SIEM-integrasjon", "Ytelsestesting & Lastsimulering", "Skykostnadsoptimalisering (FinOps)", "Automatisk skaleringspakke",
		"Arkitektur-workshop (heldag)", "Sikkerhetsopplæring for utviklere", "Lederworkshop: Digital strategi", "Onboarding-program for prosjektteam", "Design Sprint (5 dager)",
		"Sky-migrering forstudie", "GDPR & Personvern-gjennomgang", "NIS2 Beredskapsøvelse", "Kodekvalitet & Teknisk gjeld-revisjon", "AI-modenhetsvurdering",
		"SLA Gull – 24/7 Vaktordning", "SLA Sølv – Utvidet åpningstid", "SLA Bronse – Kontortid", "Dedikert Technical Account Manager", "Månedlig helsesjekk & oppdatering",
		"Prioritert feilrettingsavtale", "Kvartalsvis sikkerhetsrevisjon", "Integrasjonsvedlikehold", "Årlig beredskapsgjennomgang", "Utvidet datavarehus-støtte",
	}

	variantIDs := make([]int32, demoProductCount)
	validFrom := now.AddDate(-1, 0, 0)
	for i := 0; i < demoProductCount; i++ {
		catIdx := i / 10
		name := productNames[i]
		unit := "hour"
		baseNOK := float64(1450 + (i%8)*100)
		switch catIdx {
		case 1, 2, 4:
			unit = "month"
			baseNOK = float64(4500 + (i%10)*2500)
		case 3:
			unit = "day"
			baseNOK = float64(14500 + (i%5)*3500)
		}
		taxID := vat25ID
		if i == 49 {
			taxID = vat0ID
		}

		var prodID int32
		if err := tx.QueryRow(ctx, `
			INSERT INTO products.products (name, description, type, status, category_id, tax_category_id, created_at, updated_at)
			VALUES ($1, $2, 'service', 'active', $3, $4, $5, $5)
			RETURNING id
		`, name, fmt.Sprintf("%s – standard leveranse i henhold til avtalevilkår.", name), catIDs[catIdx], taxID, now).Scan(&prodID); err != nil {
			return nil, err
		}

		sku := fmt.Sprintf("SKU-%04d", 1001+i)
		var varID int32
		if err := tx.QueryRow(ctx, `
			INSERT INTO products.product_variants (product_id, sku, unit, standard_cost, option_values, created_at, updated_at)
			VALUES ($1, $2, $3, $4, '{}'::jsonb, $5, $5)
			ON CONFLICT (sku) DO UPDATE SET updated_at = EXCLUDED.updated_at
			RETURNING id
		`, prodID, sku, unit, baseNOK*0.45, now).Scan(&varID); err != nil {
			return nil, err
		}
		variantIDs[i] = varID

		if _, err := tx.Exec(ctx, `
			INSERT INTO products.product_prices (variant_id, currency, amount, valid_from)
			VALUES ($1, 'NOK', $2, $3)
		`, varID, baseNOK, validFrom); err != nil {
			return nil, err
		}
		if i < 15 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO products.product_prices (variant_id, currency, amount, valid_from)
				VALUES ($1, 'EUR', $2, $3)
			`, varID, baseNOK/11.5, validFrom); err != nil {
				return nil, err
			}
		}
	}

	return variantIDs, nil
}

func seedProjectsPortfolio(ctx context.Context, tx pgx.Tx, now time.Time, userIDs []uuid.UUID, customerIDs []int32, variantIDs []int32) ([]int32, []int32, error) {
	projectThemes := []string{
		"Skytransformasjon & Plattform", "EHF & Økonomiintegrasjon", "Kundeportal 2.0", "Sikkerhetsrevisjon & NIS2",
		"Dataplattform & Innsikt", "Digital Arbeidsflate", "Målepunkt-automatisering", "API-modernisering",
		"ERP-migrering", "Selvbetjeningsløsning", "Mobilapp for feltarbeid", "Arkitekturrådgivning",
		"Automatisert fakturaflyt", "Kritiske integrasjoner", "Ytelsesoptimalisering",
	}

	projectIDs := make([]int32, demoProjectCount)
	primaryBillingLines := make([]int32, demoProjectCount)

	for i := 0; i < demoProjectCount; i++ {
		code := fmt.Sprintf("PRJ-%04d", i+1)
		theme := projectThemes[i%len(projectThemes)]
		custID := customerIDs[i%len(customerIDs)]
		managerID := userIDs[i%6]
		memberA := userIDs[6+(i%7)]
		memberB := userIDs[13+(i%7)]

		status := "active"
		switch {
		case i >= 65:
			status = "completed"
		case i >= 58:
			status = "planned"
		case i >= 53:
			status = "on_hold"
		}

		billingType := "time_and_materials"
		var fixedPrice *float64
		if i%4 == 3 {
			billingType = "fixed_price"
			fp := float64(320000 + (i%6)*85000)
			fixedPrice = &fp
		}
		budgetHours := float64(160 + (i%8)*60)
		defaultRate := float64(1550 + (i%5)*100)
		budgetAmount := budgetHours * defaultRate
		startDate := now.AddDate(0, -(3 + (i % 5)), -(i % 20)).Format("2006-01-02")
		endDate := now.AddDate(0, 2+(i%6), i%15).Format("2006-01-02")

		var projID int32
		if err := tx.QueryRow(ctx, `
			INSERT INTO projects.projects (
				code, name, description, status, customer_id, billing_type,
				currency, fixed_price_amount, budget_hours, budget_amount, default_bill_rate,
				start_date, end_date, revision, created_by_user_id, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				'NOK', $7, $8, $9, $10,
				$11::date, $12::date, 1, $13, $14, $14
			)
			RETURNING id
		`,
			code, theme, fmt.Sprintf("Leveranseprosjekt for %s med mål om målbar gevinstrealisering.", theme),
			status, custID, billingType,
			fixedPrice, budgetHours, budgetAmount, defaultRate,
			startDate, endDate, managerID, now,
		).Scan(&projID); err != nil {
			return nil, nil, err
		}
		projectIDs[i] = projID

		for _, r := range []struct {
			uid  uuid.UUID
			role string
		}{
			{managerID, "manager"},
			{memberA, "member"},
			{memberB, "member"},
		} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO projects.project_roles (project_id, user_id, role, created_at)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT DO NOTHING
			`, projID, r.uid, r.role, now); err != nil {
				return nil, nil, err
			}
		}

		// Two billing lines per project (Architecture/Consulting + Engineering/Delivery).
		var line1ID int32
		if err := tx.QueryRow(ctx, `
			INSERT INTO projects.billing_lines (
				project_id, code, variant_id, pricing_mode, active,
				budget_hours, budget_amount, created_at, updated_at
			) VALUES ($1, 'RADGIV', $2, 'catalog', true, $3, $4, $5, $5)
			RETURNING id
		`, projID, variantIDs[i%10], budgetHours*0.4, budgetAmount*0.4, now).Scan(&line1ID); err != nil {
			return nil, nil, err
		}
		primaryBillingLines[i] = line1ID

		if _, err := tx.Exec(ctx, `
			INSERT INTO projects.billing_lines (
				project_id, code, variant_id, pricing_mode, fixed_amount, active,
				budget_hours, budget_amount, created_at, updated_at
			) VALUES ($1, 'UTVIKL', $2, 'fixed', $3, true, $4, $5, $6, $6)
		`, projID, variantIDs[3+(i%7)], defaultRate, budgetHours*0.6, budgetAmount*0.6, now); err != nil {
			return nil, nil, err
		}

		// Work types on the first 30 projects.
		if i < 30 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO projects.work_types (project_id, name, bill_multiplier_percent, cost_multiplier_percent, active, created_at, updated_at)
				VALUES
					($1, 'Overtid 50 %', 150.00, 150.00, true, $2, $2),
					($1, 'Overtid 100 %', 200.00, 200.00, true, $2, $2)
			`, projID, now); err != nil {
				return nil, nil, err
			}
		}

		// 3 tasks per project across todo, in_progress, done.
		taskDefs := []struct {
			title    string
			status   string
			assignee uuid.UUID
			est      float64
			offset   int
		}{
			{"Kartlegging, målbilde og teknisk spesifikasjon", "done", managerID, 24.0, -14},
			{"Implementering av kjerneløsning og integrasjoner", "in_progress", memberA, 56.0, 7},
			{"Sikkerhetsgjennomgang, ytelsestest og produksjonssetting", "todo", memberB, 32.0, 21},
		}
		for pos, td := range taskDefs {
			var completedAt *time.Time
			if td.status == "done" {
				t := now.AddDate(0, 0, -7)
				completedAt = &t
			}
			due := now.AddDate(0, 0, td.offset).Format("2006-01-02")
			var taskID int32
			if err := tx.QueryRow(ctx, `
				INSERT INTO projects.tasks (
					project_id, title, description, status, assignee_user_id,
					due_date, estimate_hours, position, completed_at,
					created_by_user_id, revision, created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6::date, $7, $8, $9, $10, 1, $11, $11)
				RETURNING id
			`, projID, td.title, "Leveranseoppgave i henhold til prosjektplan.", td.status, td.assignee, due, td.est, pos+1, completedAt, managerID, now).Scan(&taskID); err != nil {
				return nil, nil, err
			}

			if i < 20 && pos == 1 {
				if _, err := tx.Exec(ctx, `
					INSERT INTO projects.task_checklist_items (task_id, text, done, position, created_at, updated_at)
					VALUES
						($1, 'Sett opp miljø og tilgangsstyring', true, 1, $2, $2),
						($1, 'Implementer endepunkter og validering', true, 2, $2, $2),
						($1, 'Kjør ende-til-ende verifikasjon med kunde', false, 3, $2, $2)
				`, taskID, now); err != nil {
					return nil, nil, err
				}
			}
		}

		// Billing milestones for fixed-price projects.
		if billingType == "fixed_price" && fixedPrice != nil {
			m1Amt := *fixedPrice * 0.4
			m2Amt := *fixedPrice * 0.6
			m1Date := now.AddDate(0, 0, -5).Format("2006-01-02")
			m2Date := now.AddDate(0, 1, 10).Format("2006-01-02")
			if _, err := tx.Exec(ctx, `
				INSERT INTO projects.billing_milestones (
					project_id, name, description, planned_date, amount, amount_currency,
					status, position, ready_at, ready_by_user_id, created_by_user_id, revision, created_at, updated_at
				) VALUES
					($1, 'Milepæl 1: Oppstart og godkjent målbilde (40 %)', 'Faktureres ved godkjent arkitekturbeslutning', $2::date, $3, 'NOK', 'ready', 1, $4, $5, $5, 1, $4, $4),
					($1, 'Milepæl 2: Produksjonssetting og overlevering (60 %)', 'Faktureres ved godkjent akseptansetest', $6::date, $7, 'NOK', 'planned', 2, NULL, NULL, $5, 1, $4, $4)
			`, projID, m1Date, m1Amt, now, managerID, m2Date, m2Amt); err != nil {
				return nil, nil, err
			}
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO projects.counters (counter_name, next_value)
		VALUES ('project_code', $1)
		ON CONFLICT (counter_name) DO UPDATE SET next_value = GREATEST(projects.counters.next_value, EXCLUDED.next_value)
	`, demoProjectCount+1); err != nil {
		return nil, nil, err
	}

	return projectIDs, primaryBillingLines, nil
}

func seedTimeAndExpenses(ctx context.Context, tx pgx.Tx, now time.Time, userIDs []uuid.UUID, projectIDs []int32, billingLineIDs []int32) error {
	validFrom := now.AddDate(-2, 0, 0).Format("2006-01-02")
	for i, uid := range userIDs {
		m := demoStaffMembers[i]
		if _, err := tx.Exec(ctx, `
			INSERT INTO time.person_rates (user_id, currency, bill_rate, cost_rate, valid_from, created_at, updated_at)
			VALUES ($1, 'NOK', $2, $3, $4::date, $5, $5)
			ON CONFLICT (user_id, valid_from) DO UPDATE SET updated_at = EXCLUDED.updated_at
		`, uid, m.billRate, m.costRate, validFrom, now); err != nil {
			return err
		}
	}

	notes := []string{
		"Arkitekturarbeid og API-utforming med kundeteamet",
		"Utvikling av integrasjonsflyt og automatiske tester",
		"Gjennomgang av sikkerhetskrav og tilgangsstyring",
		"Arbeidsmøte og kravavklaring med produkteier",
		"Ytelsesoptimalisering av databasespørringer",
		"Forberedelse og gjennomføring av demo for styringsgruppe",
	}

	// 240 time entries across the last 45 days.
	for i := 0; i < 240; i++ {
		projIdx := i % 45 // active projects
		projID := projectIDs[projIdx]
		lineID := billingLineIDs[projIdx]
		userIdx := i % len(userIDs)
		uid := userIDs[userIdx]
		approverID := userIDs[0]
		m := demoStaffMembers[userIdx]

		daysAgo := (i / 5) + 1
		entryDate := now.AddDate(0, 0, -daysAgo).Format("2006-01-02")
		hours := float64(4 + (i % 5)) // 4..8 hours

		var status string
		var submittedAt, approvedAt, invoicedAt *time.Time
		var approvedBy *uuid.UUID
		switch {
		case i < 20:
			status = "draft"
		case i < 50:
			status = "submitted"
			submittedAt = &now
		case i < 190:
			status = "approved"
			submittedAt = &now
			approvedAt = &now
			approvedBy = &approverID
		default:
			status = "invoiced"
			submittedAt = &now
			approvedAt = &now
			invoicedAt = &now
			approvedBy = &approverID
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO time.entries (
				user_id, project_id, billing_line_id, entry_date, hours, note,
				status, billable, bill_rate, bill_currency, rate_source,
				cost_rate, cost_currency,
				submitted_at, approved_at, approved_by_user_id, invoiced_at,
				revision, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4::date, $5, $6,
				$7, true, $8, 'NOK', 'project',
				$9, 'NOK',
				$10, $11, $12, $13,
				1, $14, $14
			)
		`,
			uid, projID, lineID, entryDate, hours, notes[i%len(notes)],
			status, m.billRate, m.costRate,
			submittedAt, approvedAt, approvedBy, invoicedAt,
			now,
		); err != nil {
			return err
		}
	}

	// 30 project expenses (outlays).
	expenseDescs := []string{
		"Flyreise Oslo–Bergen i forbindelse med kundeworkshop",
		"Hotellopphold under prosjektoppstart hos kunde",
		"Faglunsj og bevertning under styringsgruppemøte",
		"Programvarelisens og testsertifikater for prosjektmiljø",
	}
	for i := 0; i < 30; i++ {
		projID := projectIDs[i%30]
		userIdx := (i + 2) % len(userIDs)
		uid := userIDs[userIdx]
		approverID := userIDs[0]
		incurredOn := now.AddDate(0, 0, -(i + 2)).Format("2006-01-02")
		amount := float64(850 + (i%7)*620)
		vat := amount * 0.20

		var status string
		var submittedAt, decidedAt, reimbursedAt *time.Time
		var decidedBy, reimbursedBy *uuid.UUID
		var paymentRef, paymentDate *string
		switch {
		case i < 6:
			status = "submitted"
			submittedAt = &now
		case i < 20:
			status = "approved"
			submittedAt = &now
			decidedAt = &now
			decidedBy = &approverID
		default:
			status = "reimbursed"
			submittedAt = &now
			decidedAt = &now
			reimbursedAt = &now
			decidedBy = &approverID
			reimbursedBy = &approverID
			ref := fmt.Sprintf("UTBET-%d", 2001+i)
			paymentRef = &ref
			paymentDate = &incurredOn
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO expenses.entries (
				user_id, created_by_user_id, project_id, kind, entry_date, description,
				status, billable, markup_percent,
				gross_amount, vat_amount, currency, bill_amount,
				paid_by, supplier,
				submitted_at, decided_at, decided_by_user_id,
				reimbursed_at, reimbursed_by_user_id, reimbursement_reference, reimbursement_date,
				revision, created_at, updated_at
			) VALUES (
				$1, $1, $2, 'outlay', $3::date, $4,
				$5, true, 0,
				$6, $7, 'NOK', $6,
				'employee', 'SAS / Strawberry',
				$8, $9, $10,
				$11, $12, $13, $14::date,
				1, $15, $15
			)
		`,
			uid, projID, incurredOn, expenseDescs[i%len(expenseDescs)],
			status, amount, vat,
			submittedAt, decidedAt, decidedBy,
			reimbursedAt, reimbursedBy, paymentRef, paymentDate,
			now,
		); err != nil {
			return err
		}
	}

	return nil
}

func seedInvoicesReceivables(ctx context.Context, tx pgx.Tx, now time.Time, userIDs []uuid.UUID, customerIDs []int32, projectIDs []int32) error {
	sellerOrg := "987654325"
	sellerBank := norwegianBankAccount("1506100001")
	ownerID := userIDs[0]

	if _, err := tx.Exec(ctx, `
		UPDATE invoices.settings
		SET legal_name = 'Vantigo Consulting AS',
		    organisation_number = $1,
		    vat_registered = true,
		    in_foretaksregisteret = true,
		    address_line1 = 'Karl Johans gate 41',
		    postal_code = '0162',
		    city = 'Oslo',
		    country = 'NO',
		    bank_account = $2,
		    iban = 'NO9386011117947',
		    bic = 'DNBANOKKXXX',
		    email = 'faktura@vantigo.local',
		    peppol_id = $3,
		    default_currency = 'NOK',
		    default_payment_terms_days = 14,
		    series_start = 10001,
		    updated_at = $4
		WHERE id = 1
	`, sellerOrg, sellerBank, "0192:"+sellerOrg, now); err != nil {
		return err
	}

	// Create 30 invoices:
	// - 0..11: issued & paid in full
	// - 12..15: issued & partially paid
	// - 16..19: issued & open (due in the future)
	// - 20..23: issued & overdue (due date in the past)
	// - 24..29: draft invoices
	const totalInvoices = 30
	nextDocNumber := int64(10001)

	for i := 0; i < totalInvoices; i++ {
		custID := customerIDs[i]
		projID := projectIDs[i]
		projRef := fmt.Sprintf("PRJ-%04d", i+1)

		qty := float64(20 + (i%8)*10)
		unitPrice := float64(1550 + (i%5)*100)
		net := qty * unitPrice
		vat := net * 0.25
		gross := net + vat

		// Always insert as draft first (00034 trigger requirement: child lines and vat_summaries must be inserted before transitioning status to 'issued').
		var invID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO invoices.invoices (
				kind, status, customer_id, currency, payment_terms_days,
				your_reference, our_reference, order_reference,
				project_id, project_reference,
				net_total, vat_total, gross_total, vat_total_nok,
				revision, created_by_user_id, created_at, updated_at
			) VALUES (
				'invoice', 'draft', $1, 'NOK', 14,
				'Innkjøpsavdeling', 'Astrid Lindqvist', $2,
				$3, $4,
				$5, $6, $7, $6,
				1, $8, $9, $9
			)
			RETURNING id
		`, custID, fmt.Sprintf("REF-%d", 1000+i), projID, projRef, net, vat, gross, ownerID, now).Scan(&invID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO invoices.lines (
				invoice_id, position, description, quantity, unit, unit_price,
				discount_percent, vat_code_id,
				line_gross, line_allowance, line_net,
				vat_rate_percent, vat_category, saf_t_code
			) VALUES (
				$1, 1, $2, $3, 'hour', $4,
				0, 1,
				$5, 0, $5,
				25.00, 'S', '3'
			)
		`, invID, fmt.Sprintf("Konsulentbistand og leveranse (%s)", projRef), qty, unitPrice, net); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO invoices.vat_summaries (
				invoice_id, vat_category, rate_percent, saf_t_code,
				taxable_amount, vat_amount, vat_amount_nok
			) VALUES ($1, 'S', 25.00, '3', $2, $3, $3)
		`, invID, net, vat); err != nil {
			return err
		}

		if i >= 24 {
			// Keep 24..29 as draft invoices.
			continue
		}

		docNum := nextDocNumber
		nextDocNumber++

		var issueDate, dueDate time.Time
		switch {
		case i < 12:
			// Paid invoices from the last 1-3 months.
			issueDate = now.AddDate(0, 0, -(45 - i*2))
			dueDate = issueDate.AddDate(0, 0, 14)
		case i < 16:
			// Partially paid invoices.
			issueDate = now.AddDate(0, 0, -(20 - (i-12)*2))
			dueDate = issueDate.AddDate(0, 0, 14)
		case i < 20:
			// Open invoices (not yet due).
			issueDate = now.AddDate(0, 0, -(4 - (i - 16)))
			dueDate = issueDate.AddDate(0, 0, 14)
		default:
			// Overdue invoices (due 5..25 days ago).
			issueDate = now.AddDate(0, 0, -(25 + (i-20)*5))
			dueDate = issueDate.AddDate(0, 0, 14)
		}

		var buyerCustNum int64
		var buyerName, buyerOrg, buyerLine1, buyerPostal, buyerCity, buyerPeppol string
		if err := tx.QueryRow(ctx, `
			SELECT c.customer_number, c.legal_name, c.legal_id, a.line1, a.postal_code, a.city, c.peppol_id
			FROM customers.customers c
			JOIN customers.customer_addresses a ON a.customer_id = c.id AND a.is_primary
			WHERE c.id = $1
		`, custID).Scan(&buyerCustNum, &buyerName, &buyerOrg, &buyerLine1, &buyerPostal, &buyerCity, &buyerPeppol); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE invoices.invoices
			SET status = 'issued',
			    number = $2,
			    issue_date = $3::date,
			    due_date = $4::date,
			    exchange_rate_date = $3::date,
			    buyer_customer_number = $5,
			    buyer_type = 'business',
			    buyer_name = $6,
			    buyer_organisation_number = $7,
			    buyer_address_line1 = $8,
			    buyer_postal_code = $9,
			    buyer_city = $10,
			    buyer_country = 'NO',
			    buyer_peppol_id = $11,
			    buyer_language = 'nb',
			    seller_legal_name = 'Vantigo Consulting AS',
			    seller_organisation_number = $12,
			    seller_vat_registered = true,
			    seller_in_foretaksregisteret = true,
			    seller_address_line1 = 'Karl Johans gate 41',
			    seller_postal_code = '0162',
			    seller_city = 'Oslo',
			    seller_country = 'NO',
			    seller_bank_account = $13,
			    seller_iban = 'NO9386011117947',
			    seller_bic = 'DNBANOKKXXX',
			    seller_email = 'faktura@vantigo.local',
			    issued_by_user_id = $14,
			    issued_at = $3,
			    updated_at = $3
			WHERE id = $1
		`,
			invID, docNum, issueDate, dueDate,
			buyerCustNum, buyerName, buyerOrg, buyerLine1, buyerPostal, buyerCity, buyerPeppol,
			sellerOrg, sellerBank, ownerID,
		); err != nil {
			return err
		}

		if i < 12 {
			paidOn := issueDate.AddDate(0, 0, 10)
			if _, err := tx.Exec(ctx, `
				INSERT INTO invoices.payments (
					invoice_id, paid_on, amount, currency, source, reference, note, registered_by_user_id, registered_at
				) VALUES ($1, $2::date, $3, 'NOK', 'manual', $4, 'Innbetalt via bank', $5, $2)
			`, invID, paidOn, gross, fmt.Sprintf("BET-%d", docNum), ownerID); err != nil {
				return err
			}
		} else if i < 16 {
			paidOn := issueDate.AddDate(0, 0, 7)
			if _, err := tx.Exec(ctx, `
				INSERT INTO invoices.payments (
					invoice_id, paid_on, amount, currency, source, reference, note, registered_by_user_id, registered_at
				) VALUES ($1, $2::date, $3, 'NOK', 'manual', $4, 'Delbetaling mottatt', $5, $2)
			`, invID, paidOn, gross*0.5, fmt.Sprintf("DEL-%d", docNum), ownerID); err != nil {
				return err
			}
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.counters (counter_name, next_value)
		VALUES ('documents', $1)
		ON CONFLICT (counter_name) DO UPDATE SET next_value = GREATEST(invoices.counters.next_value, EXCLUDED.next_value)
	`, nextDocNumber); err != nil {
		return err
	}

	return nil
}

func seedEnergyAndCommunications(ctx context.Context, tx pgx.Tx, now time.Time, userIDs []uuid.UUID, customerIDs []int32, contactIDs []int32) error {
	priceAreas := []string{"NO1", "NO2", "NO3", "NO4", "NO5"}
	cities := []struct {
		city   string
		postal string
	}{
		{"Oslo", "0150"},
		{"Kristiansand", "4611"},
		{"Trondheim", "7011"},
		{"Tromsø", "9008"},
		{"Bergen", "5013"},
	}

	for i := 0; i < 10; i++ {
		gsrn := fmt.Sprintf("70705750000000%04d", 1001+i)
		pa := priceAreas[i%len(priceAreas)]
		c := cities[i%len(cities)]
		kwh := float64(45000 + i*12500)

		var mpID int32
		if err := tx.QueryRow(ctx, `
			INSERT INTO energy.metering_points (
				gsrn, street_address, postal_code, city, country_code,
				price_area, grid_area, expected_annual_consumption_kwh,
				connection_status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, 'NO', $5, 'Elvia / BKK', $6, 'Connected', $7, $7)
			ON CONFLICT (gsrn) DO UPDATE SET updated_at = EXCLUDED.updated_at
			RETURNING id
		`, gsrn, fmt.Sprintf("Industriveien %d", 10+i), c.postal, c.city, pa, kwh, now).Scan(&mpID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO energy.meters (metering_point_id, meter_number, installed_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (metering_point_id) WHERE removed_at IS NULL DO NOTHING
		`, mpID, fmt.Sprintf("AMS-%06d", 500001+i), now.AddDate(-1, 0, 0)); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, status)
			VALUES ($1, $2, $3, 'Active')
		`, mpID, customerIDs[i], now.AddDate(0, -6, 0)); err != nil {
			return err
		}
	}

	// Seed Communications default SMTP channel, tags, and sample customer conversations.
	channelID := uuid.MustParse("40000000-0000-4000-8000-000000000001")
	if err := tx.QueryRow(ctx, `
		INSERT INTO communications.channels (id, type, address, display_name, provider, is_default, is_active, created_at)
		VALUES ($1, 'email', 'kundeservice@vantigo.local', 'Vantigo Kundeservice', 'smtp', true, true, $2)
		ON CONFLICT (type, address) DO UPDATE SET display_name = EXCLUDED.display_name
		RETURNING id
	`, channelID, now).Scan(&channelID); err != nil {
		return err
	}

	commTags := []struct {
		id    uuid.UUID
		name  string
		color string
	}{
		{uuid.MustParse("41000000-0000-4000-8000-000000000001"), "Support", "blue"},
		{uuid.MustParse("41000000-0000-4000-8000-000000000002"), "Faktura", "teal"},
		{uuid.MustParse("41000000-0000-4000-8000-000000000003"), "Prosjekt", "violet"},
		{uuid.MustParse("41000000-0000-4000-8000-000000000004"), "Haster", "red"},
	}
	for i := range commTags {
		if err := tx.QueryRow(ctx, `
			INSERT INTO communications.tags (id, name, color)
			VALUES ($1, $2, $3)
			ON CONFLICT (name) DO UPDATE SET color = EXCLUDED.color
			RETURNING id
		`, commTags[i].id, commTags[i].name, commTags[i].color).Scan(&commTags[i].id); err != nil {
			return err
		}
	}

	subjects := []string{
		"Spørsmål om EHF-fakturering og referansekode",
		"Oppstartsmøte for nytt leveranseprosjekt",
		"Utvidelse av lisenspakke og brukertilganger",
		"Bestilling av arkitektur-workshop i mai",
		"Status på integrasjon mot økonomisystem",
	}
	for i := 0; i < 10; i++ {
		custID := customerIDs[i]
		contactID := contactIDs[i*2]
		assigneeID := userIDs[i%6]
		subj := subjects[i%len(subjects)]
		actAt := now.Add(-time.Duration(i*6+1) * time.Hour)

		partID := uuid.MustParse(fmt.Sprintf("42000000-0000-4000-8000-%012d", i+1))
		if err := tx.QueryRow(ctx, `
			INSERT INTO communications.participants (id, channel_id, address, display_name, contact_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (channel_id, address) DO UPDATE SET display_name = EXCLUDED.display_name
			RETURNING id
		`, partID, channelID, fmt.Sprintf("kontakt%d@kunde%d.no", i+1, i+1), fmt.Sprintf("Kundekontakt %d", i+1), contactID, actAt).Scan(&partID); err != nil {
			return err
		}

		convID := uuid.MustParse(fmt.Sprintf("43000000-0000-4000-8000-%012d", i+1))
		preview := "Hei! Vi bekrefter mottatt bestilling og har lagt inn ønsket ressursplan for neste uke."
		if _, err := tx.Exec(ctx, `
			INSERT INTO communications.conversations (
				id, channel_id, subject, status, assigned_user_id,
				customer_id, customer_association_source, last_activity_at, preview_text, created_at
			) VALUES ($1, $2, $3, 'open', $4, $5, 'manual', $6, $7, $6)
		`, convID, channelID, subj, assigneeID, custID, actAt, preview); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO communications.conversation_participants (conversation_id, participant_id, role)
			VALUES ($1, $2, 'participant')
			ON CONFLICT DO NOTHING
		`, convID, partID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO communications.conversation_tags (conversation_id, tag_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, convID, commTags[i%len(commTags)].id); err != nil {
			return err
		}

		msgID := uuid.MustParse(fmt.Sprintf("44000000-0000-4000-8000-%012d", i+1))
		if _, err := tx.Exec(ctx, `
			INSERT INTO communications.conversation_messages (
				id, conversation_id, direction, participant_id, author_user_id,
				subject, text_body, occurred_at, created_at
			) VALUES ($1, $2, 'outbound', $3, $4, $5, $6, $7, $7)
		`, msgID, convID, partID, assigneeID, subj, preview, actAt); err != nil {
			return err
		}
	}

	return nil
}

// norwegianOrgNumber returns the nth valid 9-digit Norwegian organisation
// number (starting above 910000000) that passes the Brreg mod-11 check digit
// rule (weights 3, 2, 7, 6, 5, 4, 3, 2).
func norwegianOrgNumber(index int) string {
	weights := [8]int{3, 2, 7, 6, 5, 4, 3, 2}
	candidate := 91000000 + index*7
	for {
		base := fmt.Sprintf("%08d", candidate)
		sum := 0
		for i, w := range weights {
			sum += int(base[i]-'0') * w
		}
		rem := sum % 11
		check := 0
		if rem != 0 {
			check = 11 - rem
		}
		if check < 10 {
			return fmt.Sprintf("%s%d", base, check)
		}
		candidate++
	}
}

// norwegianBankAccount appends a valid mod-11 check digit (weights
// 5, 4, 3, 2, 7, 6, 5, 4, 3, 2) to a 10-digit account prefix. If the
// prefix would produce remainder 1 (check 10), it increments the last digit
// of the prefix.
func norwegianBankAccount(prefix10 string) string {
	weights := [10]int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	var base int64
	_, _ = fmt.Sscanf(prefix10, "%d", &base)
	for {
		s := fmt.Sprintf("%010d", base)
		sum := 0
		for i, w := range weights {
			sum += int(s[i]-'0') * w
		}
		rem := sum % 11
		check := 0
		if rem != 0 {
			check = 11 - rem
		}
		if check < 10 {
			return fmt.Sprintf("%s%d", s, check)
		}
		base++
	}
}

func asciiSlug(s string) string {
	r := strings.NewReplacer(
		"æ", "ae", "ø", "o", "å", "a",
		"Æ", "ae", "Ø", "o", "Å", "a",
		" ", "-",
	)
	return strings.ToLower(r.Replace(s))
}

func slugifyDomain(prefix, domain string, index int) string {
	return fmt.Sprintf("%s-%s-%d.no", asciiSlug(prefix), asciiSlug(domain), index+1)
}
