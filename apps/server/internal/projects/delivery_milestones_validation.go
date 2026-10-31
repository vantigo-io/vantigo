package projects

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
)

// validateDeliveryMilestoneName validates a delivery milestone name:
// non-blank, 1 to 200 characters, trimmed.
func validateDeliveryMilestoneName(raw string) (string, string) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", "A delivery milestone name cannot be null or empty"
	}
	if n := utf8.RuneCountInString(name); n > 200 {
		return "", fmt.Sprintf("A delivery milestone name cannot be longer than 200 characters, given %d", n)
	}
	return name, ""
}

// validateDeliveryMilestoneDescription validates an optional delivery milestone description:
// up to 2000 characters when trimmed.
func validateDeliveryMilestoneDescription(raw *string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, ""
	}
	if n := utf8.RuneCountInString(trimmed); n > 2000 {
		return nil, fmt.Sprintf("A delivery milestone description cannot be longer than 2000 characters, given %d", n)
	}
	return &trimmed, ""
}

// validateDeliveryMilestoneTargetDate validates optional target date.
func validateDeliveryMilestoneTargetDate(raw *openapi_types.Date) (pgtype.Date, string) {
	if raw == nil {
		return pgtype.Date{Valid: false}, ""
	}
	return dateToPgtype(raw), ""
}

// validateDeliveryMilestoneStatus validates status ("open" or "completed").
func validateDeliveryMilestoneStatus(raw string) string {
	switch raw {
	case "open", "completed":
		return ""
	default:
		return "A delivery milestone status must be 'open' or 'completed'"
	}
}

type parsedDeliveryMilestone struct {
	Name        string
	Description *string
	TargetDate  pgtype.Date
}

func validateDeliveryMilestoneCreate(body gen.DeliveryMilestoneCreateRequest) (parsedDeliveryMilestone, map[string][]string) {
	errs := map[string][]string{}
	add := func(field, msg string) {
		if msg != "" {
			errs[field] = append(errs[field], msg)
		}
	}

	name, msg := validateDeliveryMilestoneName(body.Name)
	add("name", msg)
	desc, msg := validateDeliveryMilestoneDescription(body.Description)
	add("description", msg)
	targetDate, msg := validateDeliveryMilestoneTargetDate(body.TargetDate)
	add("targetDate", msg)

	if len(errs) > 0 {
		return parsedDeliveryMilestone{}, errs
	}
	return parsedDeliveryMilestone{
		Name:        name,
		Description: desc,
		TargetDate:  targetDate,
	}, nil
}

func validateDeliveryMilestoneUpdate(body gen.DeliveryMilestoneUpdateRequest) (parsedDeliveryMilestone, map[string][]string) {
	errs := map[string][]string{}
	add := func(field, msg string) {
		if msg != "" {
			errs[field] = append(errs[field], msg)
		}
	}

	name, msg := validateDeliveryMilestoneName(body.Name)
	add("name", msg)
	desc, msg := validateDeliveryMilestoneDescription(body.Description)
	add("description", msg)
	targetDate, msg := validateDeliveryMilestoneTargetDate(body.TargetDate)
	add("targetDate", msg)

	if len(errs) > 0 {
		return parsedDeliveryMilestone{}, errs
	}
	return parsedDeliveryMilestone{
		Name:        name,
		Description: desc,
		TargetDate:  targetDate,
	}, nil
}
