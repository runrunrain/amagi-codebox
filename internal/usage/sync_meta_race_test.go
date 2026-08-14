package usage

import (
	"strconv"
	"sync"
	"testing"
)

// TestSyncSessionUsage_ReadsSyncMetaUnderLock exercises SyncSessionUsage
// against a concurrent writer that reassigns s.syncMeta under s.mu (the shape
// SyncAll uses at the end of every round). SyncSessionUsage now obtains its
// own round's meta from syncAllLocked inside the same critical section — it
// never re-reads s.syncMeta after releasing the lock — so the only s.syncMeta
// accesses under test are the locked writer vs the round bookkeeping.
//
// Run with -race:
//
//	go test -race ./internal/usage -run TestSyncSessionUsage_ReadsSyncMetaUnderLock
func TestSyncSessionUsage_ReadsSyncMetaUnderLock(t *testing.T) {
	s := &Service{} // nil db: SyncAll short-circuits; we drive s.syncMeta manually

	var wg sync.WaitGroup
	const rounds = 40
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			// Mimic the SyncAll writer: it reassigns s.syncMeta under s.mu.
			s.mu.Lock()
			s.syncMeta = SyncRunMeta{
				RecordsAdded:   int64(i),
				ProcessedCount: int64(i),
				FilesScanned:   i,
				Errors:         []string{"e" + strconv.Itoa(i)},
			}
			s.mu.Unlock()
		}(i)
		go func() {
			defer wg.Done()
			_, _ = s.SyncSessionUsage()
		}()
	}
	wg.Wait()
}