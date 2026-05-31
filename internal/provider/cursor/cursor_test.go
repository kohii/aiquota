package cursor

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/kohii/aiquota/internal/usage"
)

func TestReadLocalState_MissingDB(t *testing.T) {
	// No state.vscdb -> Cursor not installed -> NotConfigured, not a hard error.
	_, err := readLocalState(filepath.Join(t.TempDir(), "state.vscdb"))
	var nc *usage.NotConfiguredError
	if !errors.As(err, &nc) {
		t.Errorf("err = %v, want NotConfiguredError", err)
	}
}
