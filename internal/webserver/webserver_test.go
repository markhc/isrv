package webserver

import (
	"os"
	"testing"

	"github.com/markhc/isrv/internal/logging"
)

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}
