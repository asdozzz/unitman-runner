package threat

import (
	"strings"
	"testing"
)

func TestLevelForPercent(t *testing.T) {
	thresholds := DefaultThresholds()

	cases := []struct {
		percent float64
		want    string
	}{
		{0, LevelOK},
		{69.99, LevelOK},
		{70, LevelWarning},
		{84.99, LevelWarning},
		{85, LevelCritical},
		{89.99, LevelCritical},
		{90, LevelBlocker},
		{100, LevelBlocker},
	}

	for _, test := range cases {
		if got := thresholds.LevelForPercent(test.percent); got != test.want {
			t.Fatalf("LevelForPercent(%.2f) = %q, want %q", test.percent, got, test.want)
		}
	}
}

func TestThresholdsValid(t *testing.T) {
	if !DefaultThresholds().Valid() {
		t.Fatal("пороги по умолчанию должны быть валидны")
	}

	invalid := []Thresholds{
		{0, 85, 90},
		{70, 60, 90},
		{70, 85, 120},
		{70, 85, 85},
	}

	for _, value := range invalid {
		if value.Valid() {
			t.Fatalf("ожидали невалидные пороги: %+v", value)
		}
	}

	SetThresholds(Thresholds{Warning: 90, Critical: 80, Blocker: 70})
	if got := CurrentThresholds(); got != DefaultThresholds() {
		t.Fatalf("некорректные пороги должны откатываться к дефолтным, получили %+v", got)
	}
}

func TestPublishChoosesWorstLevel(t *testing.T) {
	Reset()
	SetThresholds(DefaultThresholds())
	t.Cleanup(func() {
		Reset()
		SetThresholds(DefaultThresholds())
	})

	snapshot := Publish(Snapshot{
		Memory: MemoryStat{UsedPercent: 20},
		Disks: []DiskStat{
			{Path: "/", UsedPercent: 30},
			{Path: "/var/lib/docker", UsedPercent: 91.5},
		},
	})

	if snapshot.Level != LevelBlocker {
		t.Fatalf("ожидали уровень %q, получили %q", LevelBlocker, snapshot.Level)
	}
	if !snapshot.Blocked {
		t.Fatal("снимок с диском 91.5% должен быть заблокирован")
	}
	if !strings.Contains(snapshot.BlockReason, "/var/lib/docker") {
		t.Fatalf("причина блокировки должна содержать диск, получили %q", snapshot.BlockReason)
	}
	if snapshot.Disks[1].Level != LevelBlocker {
		t.Fatalf("уровень проблемного диска = %q, want %q", snapshot.Disks[1].Level, LevelBlocker)
	}

	blocked, message := BlockedMessage()
	if !blocked || !strings.HasPrefix(message, BlockErrorKey+": ") {
		t.Fatalf("BlockedMessage() = (%v, %q)", blocked, message)
	}
}

func TestPublishBelowWarningIsNotBlocked(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	snapshot := Publish(Snapshot{
		Memory: MemoryStat{UsedPercent: 69},
		Disks:  []DiskStat{{Path: "/", UsedPercent: 69}},
	})

	if snapshot.Level != LevelOK || snapshot.Blocked {
		t.Fatalf("ожидали ok и отсутствие блокировки, получили %+v", snapshot)
	}
	if blocked, _ := BlockedMessage(); blocked {
		t.Fatal("без уровня blocker активности блокироваться не должны")
	}
}
