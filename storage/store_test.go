package storage_test

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/natexcvi/go-llm/storage"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestStoreContract(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlStore, err := storage.NewSQL(t.Context(), db, "alice")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	stores := map[string]storage.Store{"memory": &storage.Memory{}, "directory": dir, "sqlite": sqlStore}
	if dsn := os.Getenv("GO_LLM_TEST_POSTGRES"); dsn != "" {
		pg, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer pg.Close()
		stores["postgres"], err = storage.NewSQL(t.Context(), pg, "test-"+rand.Text())
		if err != nil {
			t.Fatal(err)
		}
	}
	for name, store := range stores {
		t.Run(name, func(t *testing.T) {
			if err := store.Put(t.Context(), "../other", nil); !errors.Is(err, fs.ErrInvalid) {
				t.Fatal(err)
			}
			b := []byte("one")
			if err := store.Put(t.Context(), "notes/a", b); err != nil {
				t.Fatal(err)
			}
			bounded := store.(storage.BoundedStore)
			prefix, more, err := bounded.GetPrefix(t.Context(), "notes/a", 2)
			if err != nil || !more || string(prefix) != "on" {
				t.Fatal(prefix, more, err)
			}
			list, err := bounded.ListFirst(t.Context(), 1)
			if err != nil || !slices.Equal(list, []string{"notes/a"}) {
				t.Fatal(list, err)
			}
			b[0] = 'x'
			got, err := store.Get(t.Context(), "notes/a")
			if err != nil || string(got) != "one" {
				t.Fatal(string(got), err)
			}
			if err := store.Put(t.Context(), "notes/a", []byte("two")); err != nil {
				t.Fatal(err)
			}
			keys, err := store.List(t.Context())
			if err != nil || !slices.Equal(keys, []string{"notes/a"}) {
				t.Fatal(keys, err)
			}
			if err := store.Delete(t.Context(), "notes/a"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(t.Context(), "notes/a"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
		})
	}
}
