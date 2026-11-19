package state

import (
	"path/filepath"
	"testing"
)

func TestBoltStoreCRUDAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	first := UnitState{
		UnitID:            "unit-1",
		ProjectID:         "project-1",
		UnitName:          "unit-one",
		ProjectName:       "project-one",
		ContainerName:     "unit-one.project-one",
		Expected:          ExpectedRunning,
		ExpectedUpdatedAt: 100,
	}
	second := UnitState{
		UnitID:            "unit-2",
		ProjectID:         "project-1",
		UnitName:          "unit-two",
		ProjectName:       "project-one",
		ContainerName:     "unit-two.project-one",
		Expected:          ExpectedAbsent,
		ExpectedUpdatedAt: 200,
	}

	if err := store.Upsert(first); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	if err := store.Upsert(second); err != nil {
		t.Fatalf("Upsert second: %v", err)
	}

	got, found, err := store.Get("unit-1")
	if err != nil {
		t.Fatalf("Get existing: %v", err)
	}
	if !found {
		t.Fatal("Get existing: expected found=true")
	}
	if got.UnitID != first.UnitID || got.ContainerName != first.ContainerName || got.Expected != first.Expected {
		t.Fatalf("Get existing: unexpected state: %+v", got)
	}

	if _, found, err := store.Get("missing"); err != nil {
		t.Fatalf("Get missing: %v", err)
	} else if found {
		t.Fatal("Get missing: expected found=false")
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List: expected 2 items, got %d: %+v", len(list), list)
	}

	// SetActual не должен создавать новую запись.
	if err := store.SetActual("missing", ActualRunning, 10); err != nil {
		t.Fatalf("SetActual missing: %v", err)
	}
	if _, found, err := store.Get("missing"); err != nil {
		t.Fatalf("Get after SetActual missing: %v", err)
	} else if found {
		t.Fatal("SetActual created a record for a non-existing unit")
	}

	// SetActual обновляет существующую запись.
	if err := store.SetActual("unit-1", ActualRunning, 123); err != nil {
		t.Fatalf("SetActual existing: %v", err)
	}
	got, found, err = store.Get("unit-1")
	if err != nil || !found {
		t.Fatalf("Get after SetActual: found=%v err=%v", found, err)
	}
	if got.Actual != ActualRunning || got.ActualCheckedAt != 123 {
		t.Fatalf("SetActual: unexpected state: %+v", got)
	}
	if got.Expected != ExpectedRunning {
		t.Fatalf("SetActual must not change expected state: %+v", got)
	}

	// MarkNotified фиксирует факт уведомления.
	if err := store.MarkNotified("unit-1", ActualAbsent, 456); err != nil {
		t.Fatalf("MarkNotified: %v", err)
	}
	got, found, err = store.Get("unit-1")
	if err != nil || !found {
		t.Fatalf("Get after MarkNotified: found=%v err=%v", found, err)
	}
	if got.LastNotifiedActual != ActualAbsent || got.LastNotifiedAt != 456 {
		t.Fatalf("MarkNotified: unexpected state: %+v", got)
	}

	// MarkNotified не создаёт запись для несуществующего юнита.
	if err := store.MarkNotified("missing", ActualRunning, 1); err != nil {
		t.Fatalf("MarkNotified missing: %v", err)
	}
	if _, found, err := store.Get("missing"); err != nil {
		t.Fatalf("Get after MarkNotified missing: %v", err)
	} else if found {
		t.Fatal("MarkNotified created a record for a non-existing unit")
	}

	if err := store.Delete("unit-2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := store.Get("unit-2"); err != nil {
		t.Fatalf("Get after Delete: %v", err)
	} else if found {
		t.Fatal("Delete: record still exists")
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Переоткрытие того же пути должно сохранить данные.
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("Open after close: %v", err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened: %v", err)
		}
	}()

	got, found, err = reopened.Get("unit-1")
	if err != nil || !found {
		t.Fatalf("Get after reopen: found=%v err=%v", found, err)
	}
	if got.Actual != ActualRunning || got.ActualCheckedAt != 123 {
		t.Fatalf("reopen lost actual state: %+v", got)
	}
	if got.LastNotifiedActual != ActualAbsent || got.LastNotifiedAt != 456 {
		t.Fatalf("reopen lost notification state: %+v", got)
	}

	if _, found, err := reopened.Get("unit-2"); err != nil {
		t.Fatalf("Get deleted after reopen: %v", err)
	} else if found {
		t.Fatal("deleted unit reappeared after reopen")
	}
}
