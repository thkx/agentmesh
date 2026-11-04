package checkpoint_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/thkx/agentmesh/internal/platform/database"
	"github.com/thkx/agentmesh/internal/workflow/checkpoint"
)

// TestM11WorkerProcessHelper is launched as a separate OS process by the
// parent test. It intentionally stays alive after claiming so the parent can
// SIGKILL it while it owns a real SQLite lease.
func TestM11WorkerProcessHelper(t *testing.T) {
	if os.Getenv("AGENTMESH_M11_HELPER") != "1" {
		return
	}
	dbPath := os.Getenv("AGENTMESH_M11_DB")
	resultPath := os.Getenv("AGENTMESH_M11_RESULT")
	owner := os.Getenv("AGENTMESH_M11_OWNER")
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	store, err := checkpoint.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimRun(context.Background(), owner, 4*time.Second)
	if err != nil {
		if err == sql.ErrNoRows {
			_ = os.WriteFile(resultPath, []byte(`{"claimed":false}`), 0600)
		} else {
			t.Fatal(err)
		}
	} else {
		b, _ := json.Marshal(map[string]any{"claimed": true, "run_id": job.RunID, "owner": job.Owner, "fence": job.FenceToken})
		if err = os.WriteFile(resultPath, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	select {}
}

func TestM11MultiProcessClaimAndSIGKILLRecovery(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "m11-process.db")
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	sharedDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	sharedDB.SetMaxOpenConns(4)
	if err = database.RunMigrations(sharedDB); err != nil {
		t.Fatal(err)
	}
	sharedStore, err := checkpoint.NewSQLite(sharedDB)
	if err != nil {
		t.Fatal(err)
	}
	createM9Run(t, sharedStore, "m11-process-run")
	defer sharedDB.Close()

	const workers = 4
	cmds := make([]*exec.Cmd, 0, workers)
	resultFiles := make([]string, 0, workers)
	for i := 0; i < workers; i++ {
		result := filepath.Join(t.TempDir(), fmt.Sprintf("worker-%d.json", i))
		cmd := exec.Command(os.Args[0], "-test.run=^TestM11WorkerProcessHelper$")
		cmd.Env = append(os.Environ(), "AGENTMESH_M11_HELPER=1", "AGENTMESH_M11_DB="+dbPath, "AGENTMESH_M11_RESULT="+result, fmt.Sprintf("AGENTMESH_M11_OWNER=process-%d", i))
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
		resultFiles = append(resultFiles, result)
	}
	defer func() {
		for _, cmd := range cmds {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				_, _ = cmd.Process.Wait()
			}
		}
	}()

	deadline := time.Now().Add(15 * time.Second)
	for {
		ready := 0
		for _, path := range resultFiles {
			if _, e := os.Stat(path); e == nil {
				ready++
			}
		}
		if ready == workers {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d worker processes reported claim outcome", ready, workers)
		}
		time.Sleep(25 * time.Millisecond)
	}
	claimed := 0
	var oldFence int64
	for _, path := range resultFiles {
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(b), `"claimed":true`) {
			claimed++
			var result struct {
				Fence int64 `json:"fence"`
			}
			if e = json.Unmarshal(b, &result); e != nil {
				t.Fatal(e)
			}
			oldFence = result.Fence
		}
	}
	if claimed != 1 {
		t.Fatalf("expected exactly one cross-process claimant, got %d", claimed)
	}

	// Kill all processes, including the sole lease owner, with SIGKILL.
	for _, cmd := range cmds {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	for _, cmd := range cmds {
		if cmd.Process != nil {
			_, _ = cmd.Process.Wait()
			cmd.Process = nil
		}
	}
	// The helper used a four-second lease. Wait for it to expire, then prove
	// another process can take over with a strictly newer fencing generation.
	time.Sleep(4200 * time.Millisecond)
	recovered, err := sharedStore.ClaimRun(context.Background(), "post-sigkill-recovery", 5*time.Second)
	if err != nil {
		t.Fatalf("claim after SIGKILL: %v", err)
	}
	if recovered.FenceToken <= oldFence {
		t.Fatalf("fencing generation did not advance: old=%d new=%d", oldFence, recovered.FenceToken)
	}
	run, err := sharedStore.GetRun(context.Background(), "m11-process-run")
	if err != nil {
		t.Fatal(err)
	}
	if err = sharedStore.SaveRunFenced(context.Background(), run, "stale-worker", oldFence); err == nil {
		t.Fatal("stale process was allowed to write checkpoint after SIGKILL recovery")
	}
}
