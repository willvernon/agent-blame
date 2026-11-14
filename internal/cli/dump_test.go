package cli

import (
	"strings"
	"testing"
	"time"

	"agent-blame/internal/sources/claudecode"
	"agent-blame/internal/testutil"
)

type prefixResolver string

func (r prefixResolver) Rel(p string) (string, bool) {
	if rest, ok := strings.CutPrefix(p, string(r)+"/"); ok {
		return rest, true
	}
	return p, false
}

func TestDumpFixtureGolden(t *testing.T) {
	m := claudecode.NewMapper(prefixResolver("/repo"))
	if _, err := claudecode.ReadFile("../sources/claudecode/testdata/session.jsonl", m.Add); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	FormatEvents(&b, m.Events(), time.UTC)
	testutil.Golden(t, "dump.txt", b.String())
}
