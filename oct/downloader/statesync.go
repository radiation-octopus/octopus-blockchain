package downloader

import (
	"sync"

	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/log"
)

// syncState starts downloading state with the given root hash.
func (d *Downloader) syncState(root entity.Hash) *stateSync {
	// Create the state sync
	s := newStateSync(d, root)
	select {
	case d.stateSyncStart <- s:
		// If we tell the statesync to restart with a new root, we also need
		// to wait for it to actually also start -- when old requests have timed
		// out or been delivered
		<-s.started
	case <-d.quitCh:
		s.err = errCancelStateFetch
		close(s.done)
	}
	return s
}

// stateFetcher管理活动状态同步并代表其接受请求。
func (d *Downloader) stateFetcher() {
	for {
		select {
		case s := <-d.stateSyncStart:
			for next := s; next != nil; {
				next = d.runStateSync(next)
			}
		case <-d.quitCh:
			return
		}
	}
}

// runStateSync运行状态同步，直到完成或请求切换到另一个根哈希。
func (d *Downloader) runStateSync(s *stateSync) *stateSync {
	log.Trace("State sync starting", "root", s.root)

	go s.run()
	defer s.Cancel()

	for {
		select {
		case next := <-d.stateSyncStart:
			return next

		case <-s.done:
			return nil
		}
	}
}

// stateSync schedules requests for downloading a particular state trie defined
// by a given state root.
type stateSync struct {
	d    *Downloader // Downloader instance to access and manage current peerset
	root entity.Hash // State root currently being synced

	started    chan struct{} // Started is signalled once the sync loop starts
	cancel     chan struct{} // Channel to signal a termination request
	cancelOnce sync.Once     // Ensures cancel only ever gets called once
	done       chan struct{} // Channel to signal termination completion
	err        error         // Any error hit during sync (set before completion)
}

// newStateSync creates a new state trie download scheduler. This method does not
// yet start the sync. The user needs to call run to initiate.
func newStateSync(d *Downloader, root entity.Hash) *stateSync {
	return &stateSync{
		d:       d,
		root:    root,
		cancel:  make(chan struct{}),
		done:    make(chan struct{}),
		started: make(chan struct{}),
	}
}

// run starts the task assignment and response processing loop, blocking until
// it finishes, and finally notifying any goroutines waiting for the loop to
// finish.
func (s *stateSync) run() {
	close(s.started)
	s.err = s.d.SnapSyncer.Sync(s.root, s.cancel)
	close(s.done)
}

// Wait blocks until the sync is done or canceled.
func (s *stateSync) Wait() error {
	<-s.done
	return s.err
}

// Cancel cancels the sync and waits until it has shut down.
func (s *stateSync) Cancel() error {
	s.cancelOnce.Do(func() {
		close(s.cancel)
	})
	return s.Wait()
}
