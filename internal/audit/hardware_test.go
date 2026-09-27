package audit

import (
	"testing"
	"time"
)

func TestHardwareVocabulary(t *testing.T) {
	for _, et := range []EventType{HardwareReported, HardwareUpdated} {
		if !Known(string(et)) {
			t.Errorf("%s not known", et)
		}
	}
	if HardwareReported != "hardware_reported" || HardwareUpdated != "hardware_updated" {
		t.Fatal("action names are part of the contract")
	}
}

// TestHardwareDetailKeysSurviveGuard: every key the hardware planner writes
// passes the detail guard (none carries a guarded substring).
func TestHardwareDetailKeysSurviveGuard(t *testing.T) {
	detail := map[string]any{
		"changes": []any{
			map[string]any{"field": "bios.version", "change": "changed", "before": "2.5", "after": "2.6"},
			map[string]any{"field": "system.serial", "change": "changed", "before": "A", "after": "B"},
		},
		"changes_truncated": 3,
		"summary": map[string]any{"cpu_model": "Xeon", "cpu_cores": 24, "memory_total_bytes": 1, "memory_type": "DDR4",
			"disk_count": 2, "disk_total_bytes": 3},
		"inventory_host_id": "h", "run_id": "r", "trigger": "poll",
	}
	row, err := Row(Event{TenantID: "t", EventType: HardwareUpdated, ActorKind: ActorSystem, ActorID: HostSyncActor,
		SubjectKind: SubjectDevice, SubjectID: "d", Outcome: OutcomeOK, Details: detail}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"changes", "changes_truncated", "summary", "inventory_host_id", "run_id", "trigger"} {
		if _, ok := row.Detail[k]; !ok {
			t.Errorf("key %q dropped by the guard", k)
		}
	}
	ch := row.Detail["changes"].([]any)[0].(map[string]any)
	for _, k := range []string{"field", "change", "before", "after"} {
		if _, ok := ch[k]; !ok {
			t.Errorf("change key %q dropped", k)
		}
	}
	sum := row.Detail["summary"].(map[string]any)
	if len(sum) != 6 {
		t.Errorf("summary keys dropped: %v", sum)
	}
}
