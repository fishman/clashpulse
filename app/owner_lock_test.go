package app

import (
 "errors"
 "testing"
)

func TestOwnerLockRejectsConcurrentOwnerAndReleases(t *testing.T) {
 home := t.TempDir()
 release, err := acquireOwnerLock(home)
 if err != nil { t.Fatal(err) }
 if second, err := acquireOwnerLock(home); !errors.Is(err, errStateInUse) {
  if second != nil { second() }
  t.Fatalf("concurrent owner was not rejected: %v", err)
 }
 release()
 next, err := acquireOwnerLock(home)
 if err != nil { t.Fatalf("state lock survived owner shutdown: %v", err) }
 next()
}

func TestRunAtRefusesStateOwnedByOfflineRefresh(t *testing.T) {
 home := t.TempDir()
 release, err := acquireOwnerLock(home)
 if err != nil { t.Fatal(err) }
 defer release()
 err = RunAt(t.Context(), t.TempDir(), home, t.TempDir()+"/ipc.sock")
 if !errors.Is(err, errStateInUse) { t.Fatalf("desktop started alongside offline owner: %v", err) }
}
