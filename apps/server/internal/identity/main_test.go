package identity_test

import (
	"os"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder))
}
