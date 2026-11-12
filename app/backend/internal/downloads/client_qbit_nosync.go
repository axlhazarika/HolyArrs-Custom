package downloads

import (
	"errors"
	"log"
	"sync"
)

// Some qBittorrent-compatible Downloaders (Decypharr, a debrid bridge) answer
// torrents/info but have no change feed: sync/maindata answers 404. That is
// not a failed Downloader, so the reader remembers it and reads torrents/info
// instead, as it does for the list-only adapters. The mark lasts for one
// engine generation: changing the Downloader tries the feed again.

type noFeedKey struct {
	s *Sync
	k cursorKey
}

var noFeed sync.Map // noFeedKey -> struct{}

// mainFeedMissing reports whether Downloader id answered 404 to the change
// feed under engine generation gen.
func (s *Sync) mainFeedMissing(id int64, gen uint64) bool {
	_, ok := noFeed.Load(noFeedKey{s, cursorKey{id, gen}})
	return ok
}

// noteMainFeedMissing marks the Downloader when err is the change feed's 404
// and says whether it did. Every other error is left to the caller.
func (s *Sync) noteMainFeedMissing(id int64, gen uint64, err error) bool {
	if !errors.Is(err, ErrNotFound) {
		return false
	}
	if _, had := noFeed.LoadOrStore(noFeedKey{s, cursorKey{id, gen}}, struct{}{}); !had {
		log.Printf("downloaders: %d has no change feed (sync/maindata answered 404); reading torrents/info instead", id)
	}
	return true
}
