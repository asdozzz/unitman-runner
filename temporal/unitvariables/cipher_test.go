package unitvariables

import (
	"encoding/hex"
	"testing"

	"runner/temporal/activity/unit/model"
)

// TestDeriveKeyMatchesBackendHKDF фиксирует вектор PHP
// hash_hkdf('sha256', runnerSecret, 32, 'unitman-unit-variables-v1').
func TestDeriveKeyMatchesBackendHKDF(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	want := "83dbd9546aec464d8b82c7a149242a9d374653fe933e31d9a6424d465944aefc"

	got := hex.EncodeToString(DeriveKey(secret))
	if got != want {
		t.Fatalf("DeriveKey() = %s, want %s", got, want)
	}
}

// TestDecryptBackendVector проверяет ciphertext, созданный PHP
// UnitVariableCipher (nonce 000102...0a0b, AAD unit-1:PV_SECRET).
func TestDecryptBackendVector(t *testing.T) {
	const runnerSecret = "f723131072c61a31b7ad019aeea57965b155050564ad2e3d8dc62fdc6a164f6e"
	const ciphertext = "AAECAwQFBgcICQoL8F7S9TOiY2Vmngr0FjyroeRy59TNfPCFU7XNOw=="

	got, err := Decrypt(ciphertext, "unit-1:PV_SECRET", runnerSecret)
	if err != nil {
		t.Fatalf("Decrypt() error: %v", err)
	}
	if got != "s3cr3t-value" {
		t.Fatalf("Decrypt() = %q, want %q", got, "s3cr3t-value")
	}
}

func TestResolveExpandsHiddenValuesOnly(t *testing.T) {
	const runnerSecret = "f723131072c61a31b7ad019aeea57965b155050564ad2e3d8dc62fdc6a164f6e"

	variables := []model.UnitConfigVariable{
		{Id: "PV_SECRET", Hidden: true, ValueCipher: "AAECAwQFBgcICQoL8F7S9TOiY2Vmngr0FjyroeRy59TNfPCFU7XNOw=="},
		{Id: "PV_PUBLIC", Hidden: false, Value: "visible"},
	}

	resolved, err := Resolve(variables, "unit-1", runnerSecret)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	if resolved[0].Value != "s3cr3t-value" {
		t.Fatalf("hidden Value = %q", resolved[0].Value)
	}
	if resolved[0].ValueCipher != "" {
		t.Fatal("hidden ValueCipher must be cleared after resolve")
	}
	if resolved[1].Value != "visible" {
		t.Fatalf("public Value = %q", resolved[1].Value)
	}
}

func TestResolveRejectsHiddenWithoutCipher(t *testing.T) {
	_, err := Resolve(
		[]model.UnitConfigVariable{{Id: "PV_SECRET", Hidden: true}},
		"unit-1",
		"f723131072c61a31b7ad019aeea57965b155050564ad2e3d8dc62fdc6a164f6e",
	)
	if err == nil {
		t.Fatal("expected an error for hidden variable without cipher")
	}
}
