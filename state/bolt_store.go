package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	unitsBucket = "units"
	metaBucket  = "meta"
)

// boltStore — реализация Store поверх bbolt.
type boltStore struct {
	db *bolt.DB
}

// Open открывает bbolt-базу по указанному пути и создаёт необходимые bucket'ы.
func Open(path string) (Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("state: create directory %q: %w", dir, err)
		}
	}

	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("state: open boltdb %q: %w", path, err)
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(unitsBucket)); err != nil {
			return fmt.Errorf("state: create bucket %q: %w", unitsBucket, err)
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(metaBucket)); err != nil {
			return fmt.Errorf("state: create bucket %q: %w", metaBucket, err)
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &boltStore{db: db}, nil
}

// Get возвращает состояние юнита по его идентификатору.
func (s *boltStore) Get(unitID string) (UnitState, bool, error) {
	var result UnitState
	found := false

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(unitsBucket))
		if bucket == nil {
			return nil
		}

		raw := bucket.Get([]byte(unitID))
		if raw == nil {
			return nil
		}

		copied := make([]byte, len(raw))
		copy(copied, raw)

		if err := json.Unmarshal(copied, &result); err != nil {
			return fmt.Errorf("state: unmarshal unit %q: %w", unitID, err)
		}
		found = true

		return nil
	})

	return result, found, err
}

// List возвращает состояния всех юнитов.
func (s *boltStore) List() ([]UnitState, error) {
	result := make([]UnitState, 0)

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(unitsBucket))
		if bucket == nil {
			return nil
		}

		return bucket.ForEach(func(_, raw []byte) error {
			var item UnitState
			if err := json.Unmarshal(raw, &item); err != nil {
				return fmt.Errorf("state: unmarshal unit: %w", err)
			}
			result = append(result, item)
			return nil
		})
	})

	return result, err
}

// Upsert сохраняет состояние юнита.
func (s *boltStore) Upsert(item UnitState) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("state: marshal unit %q: %w", item.UnitID, err)
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(unitsBucket))
		if bucket == nil {
			return fmt.Errorf("state: bucket %q not found", unitsBucket)
		}
		return bucket.Put([]byte(item.UnitID), raw)
	})
}

// Delete удаляет состояние юнита.
func (s *boltStore) Delete(unitID string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(unitsBucket))
		if bucket == nil {
			return nil
		}
		return bucket.Delete([]byte(unitID))
	})
}

// SetActual обновляет только фактическое состояние существующей записи.
func (s *boltStore) SetActual(unitID string, actual ActualState, checkedAt int64) error {
	return s.updateUnit(unitID, func(item *UnitState) {
		item.Actual = actual
		item.ActualCheckedAt = checkedAt
	})
}

// MarkNotified фиксирует факт уведомления о расхождении.
func (s *boltStore) MarkNotified(unitID string, actual ActualState, at int64) error {
	return s.updateUnit(unitID, func(item *UnitState) {
		item.LastNotifiedActual = actual
		item.LastNotifiedAt = at
	})
}

// Close закрывает bbolt-базу.
func (s *boltStore) Close() error {
	return s.db.Close()
}

// updateUnit изменяет существующую запись, не создавая новую.
func (s *boltStore) updateUnit(unitID string, mutate func(*UnitState)) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(unitsBucket))
		if bucket == nil {
			return nil
		}

		raw := bucket.Get([]byte(unitID))
		if raw == nil {
			return nil
		}

		var item UnitState
		if err := json.Unmarshal(raw, &item); err != nil {
			return fmt.Errorf("state: unmarshal unit %q: %w", unitID, err)
		}

		mutate(&item)

		updated, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("state: marshal unit %q: %w", unitID, err)
		}

		return bucket.Put([]byte(unitID), updated)
	})
}
