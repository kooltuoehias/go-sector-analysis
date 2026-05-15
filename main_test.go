package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeCSVNamesIn(t *testing.T) {
	t.Run("renames single dated file to canonical", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "omxspi_20260515_132602.csv"))

		normalizeCSVNamesIn(dir)

		assertExists(t, filepath.Join(dir, "omxspi.csv"))
		assertNotExists(t, filepath.Join(dir, "omxspi_20260515_132602.csv"))
	})

	t.Run("keeps latest and removes older when multiple dated files exist", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "sx50pi_20260315_000000.csv"))
		touch(t, filepath.Join(dir, "sx50pi_20260515_132622.csv"))

		normalizeCSVNamesIn(dir)

		assertExists(t, filepath.Join(dir, "sx50pi.csv"))
		assertNotExists(t, filepath.Join(dir, "sx50pi_20260315_000000.csv"))
		assertNotExists(t, filepath.Join(dir, "sx50pi_20260515_132622.csv"))
	})

	t.Run("no-op when canonical name already present", func(t *testing.T) {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, "omxspi.csv"))

		normalizeCSVNamesIn(dir)

		assertExists(t, filepath.Join(dir, "omxspi.csv"))
	})

	t.Run("no-op when directory has no matching csv files", func(t *testing.T) {
		dir := t.TempDir()
		normalizeCSVNamesIn(dir) // must not panic
	})

	t.Run("handles all five sector files independently", func(t *testing.T) {
		dir := t.TempDir()
		for _, pair := range csvPairs {
			dated := pair[1][:len(pair[1])-4] + "_20260515_120000.csv"
			touch(t, filepath.Join(dir, dated))
		}

		normalizeCSVNamesIn(dir)

		for _, pair := range csvPairs {
			assertExists(t, filepath.Join(dir, pair[1]))
			dated := pair[1][:len(pair[1])-4] + "_20260515_120000.csv"
			assertNotExists(t, filepath.Join(dir, dated))
		}
	})
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("expected %s to exist", path)
	}
}

func assertNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("expected %s to not exist", path)
	}
}
