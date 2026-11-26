package preflight

import (
	"encoding/json"
	"testing"
)

func TestHasRuntime(t *testing.T) {
	runtimes := map[string]json.RawMessage{
		"runc":        json.RawMessage(`{"path":"runc"}`),
		"sysbox-runc": json.RawMessage(`{"path":"sysbox-runc"}`),
	}

	if !HasRuntime(runtimes, RequiredRuntime) {
		t.Fatalf("expected %q to be present", RequiredRuntime)
	}
	if HasRuntime(runtimes, "runsc") {
		t.Fatal("runsc must not be reported as present")
	}
	if HasRuntime(nil, RequiredRuntime) {
		t.Fatal("nil runtimes must not contain sysbox-runc")
	}
}
