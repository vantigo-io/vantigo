package projects

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is design §4.2's code suggestion: a mnemonic built from the
// customer and project names plus the next unused counter value, offered as
// a starting point the caller may accept or type over. The letter derivation
// (words, lettersFor, leadingLetters, customerLetters) is pure and tested on
// its own in suggestion_internal_test.go; codeCandidate and the handler are
// the only parts that touch the database.

const (
	// internalPrefix is an internal project's customer prefix — there is no
	// customer name to derive letters from (design §4.2).
	internalPrefix = "INT"

	// codeSuggestionCounterFloor is what a suggestion offers before any
	// project has ever been created: NextCounterValue's own first
	// allocation, which PeekCounterValue never sees a row for.
	codeSuggestionCounterFloor = 1000

	// codeSuggestionMaxTries bounds the bump loop. A hand-typed code could
	// occupy any run of consecutive candidates; past this many tries the
	// endpoint answers its last candidate and lets create's own uniqueness
	// validation speak.
	codeSuggestionMaxTries = 50

	// maxProjectCodeLength is projectCodePattern's own cap (design D2): a
	// candidate longer than this could never pass validateProjectCode.
	maxProjectCodeLength = 20
)

// letterReplacer spells out the Norwegian letters before diacritics are stripped.
var letterReplacer = strings.NewReplacer("Æ", "AE", "Ø", "O", "Å", "A", "æ", "AE", "ø", "O", "å", "A")

// words splits name into upper-case A–Z words: Norwegian letters spelled out,
// other diacritics stripped (NFD, drop marks), anything else a separator.
func words(name string) []string {
	decomposed := norm.NFD.String(letterReplacer.Replace(name))
	var out []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			out = append(out, current.String())
			current.Reset()
		}
	}
	for _, r := range decomposed {
		switch {
		case unicode.Is(unicode.Mn, r):
			// a combining mark left by NFD: drop it, stay in the word
		case r >= 'a' && r <= 'z':
			current.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z':
			current.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// lettersFor is the mnemonic for a name: the first letter of each of its
// first three words, or the first two letters of a one-word name.
func lettersFor(name string) string {
	w := words(name)
	switch len(w) {
	case 0:
		return ""
	case 1:
		if len(w[0]) < 2 {
			return w[0]
		}
		return w[0][:2]
	}
	if len(w) > 3 {
		w = w[:3]
	}
	var b strings.Builder
	for _, word := range w {
		b.WriteByte(word[0])
	}
	return b.String()
}

// leadingLetters is the run of letters a code starts with ("KVEM1000" → "KVEM").
func leadingLetters(code string) string {
	i := 0
	for i < len(code) && code[i] >= 'A' && code[i] <= 'Z' {
		i++
	}
	return code[:i]
}

// customerLetters keeps a hand-chosen customer prefix sticky: the derived
// letters, unless the customer's existing codes agree on a different prefix.
func customerLetters(derived string, existingCodes []string) string {
	if len(existingCodes) == 0 || derived == "" {
		return derived
	}
	runs := make([]string, 0, len(existingCodes))
	for _, code := range existingCodes {
		run := leadingLetters(code)
		if strings.HasPrefix(run, derived) {
			return derived
		}
		runs = append(runs, run)
	}
	common := runs[0]
	for _, run := range runs[1:] {
		for !strings.HasPrefix(run, common) {
			common = common[:len(common)-1]
		}
	}
	if len(common) < 2 {
		return derived
	}
	if len(common) > len(derived) {
		common = common[:len(derived)]
	}
	return common
}

// codeCandidate is one prefix+letters+number code, shortened to fit
// maxProjectCodeLength when it would not otherwise: the project letters are
// trimmed first, then the customer prefix, and the number is never touched —
// a suggestion with the right number and a squeezed prefix is still useful,
// one with the wrong number is not (task 6 brief).
func codeCandidate(customer, project string, number int64) string {
	digits := strconv.FormatInt(number, 10)
	budget := maxProjectCodeLength - len(digits)
	if budget < 0 {
		budget = 0
	}
	if over := len(customer) + len(project) - budget; over > 0 {
		trim := min(over, len(project))
		project = project[:len(project)-trim]
	}
	if over := len(customer) + len(project) - budget; over > 0 {
		trim := min(over, len(customer))
		customer = customer[:len(customer)-trim]
	}
	return customer + project + digits
}

// GetProjectsCodeSuggestion Suggest a project code
// (GET /api/v1/projects/code-suggestion)
//
// The prefix is the customer's letters (customerLetters, sticky to a
// hand-chosen convention already in use) or "INT" for an internal project;
// the letters are the project name's own mnemonic; the number is the
// project_code counter's next value, read without allocating — it only
// advances on an actual create (projects.go), so calling this endpoint any
// number of times never changes what the next real project gets. A taken
// candidate — a code somebody typed by hand rather than accepted the
// suggestion for — is skipped by bumping the number, bounded at
// codeSuggestionMaxTries.
func (s *server) GetProjectsCodeSuggestion(ctx context.Context, req gen.GetProjectsCodeSuggestionRequestObject) (gen.GetProjectsCodeSuggestionResponseObject, error) {
	q := store.New(s.deps.Pool)

	prefix := internalPrefix
	if req.Params.CustomerId != nil {
		customer, err := s.deps.Directory.Customer(ctx, *req.Params.CustomerId)
		if err != nil {
			return nil, fmt.Errorf("projects: look up a customer for a code suggestion: %w", err)
		}
		if customer == nil {
			return gen.GetProjectsCodeSuggestion400ApplicationProblemPlusJSONResponse(apicommon.Problem(
				"Invalid code suggestion query",
				fmt.Sprintf("Customer %d does not exist", *req.Params.CustomerId),
			)), nil
		}
		codes, err := q.RecentProjectCodesForCustomer(ctx, req.Params.CustomerId)
		if err != nil {
			return nil, fmt.Errorf("projects: list a customer's recent project codes: %w", err)
		}
		prefix = customerLetters(lettersFor(customer.Name), codes)
	}

	name := ""
	if req.Params.Name != nil {
		name = *req.Params.Name
	}
	letters := lettersFor(name)

	number, err := q.PeekCounterValue(ctx, counterProjectCode)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		number = codeSuggestionCounterFloor
	case err != nil:
		return nil, fmt.Errorf("projects: peek the project code counter: %w", err)
	}

	code := codeCandidate(prefix, letters, number)
	for try := 0; try < codeSuggestionMaxTries; try++ {
		taken, err := q.ProjectCodeExists(ctx, code)
		if err != nil {
			return nil, fmt.Errorf("projects: check a code suggestion for uniqueness: %w", err)
		}
		if !taken {
			break
		}
		number++
		code = codeCandidate(prefix, letters, number)
	}

	return gen.GetProjectsCodeSuggestion200JSONResponse{Code: code}, nil
}
