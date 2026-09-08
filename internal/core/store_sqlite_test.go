package core

import (
	"path/filepath"
	"testing"
)

func TestSQLiteStorePersistsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := (&Engine{Store: store}).CreateWorkspace("sqlite", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, ok := reopened.Workspace(workspace.ID); !ok || got.Name != "sqlite" {
		t.Fatalf("workspace was not persisted: %+v, %v", got, ok)
	}
}
