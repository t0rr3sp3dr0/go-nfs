package helpers

import (
	"bytes"
	"io/fs"
	"math/rand"

	"github.com/willscott/go-nfs"

	"github.com/go-git/go-billy/v5"
	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru"
)

// NewCachingHandler wraps a handler to provide a basic to/from-file handle cache.
func NewCachingHandler(h nfs.Handler, limit int) nfs.Handler {
	cache, _ := lru.New(limit)
	verifiers, _ := lru.New(limit)
	return &CachingHandler{
		Handler:         h,
		activeHandles:   cache,
		activeVerifiers: verifiers,
		cacheLimit:      limit,
	}
}

// NewCachingHandlerWithVerifierLimit provides a basic to/from-file handle cache that can be tuned with a smaller cache of active directory listings.
func NewCachingHandlerWithVerifierLimit(h nfs.Handler, limit int, verifierLimit int) nfs.Handler {
	cache, _ := lru.New(limit)
	verifiers, _ := lru.New(verifierLimit)
	return &CachingHandler{
		Handler:         h,
		activeHandles:   cache,
		activeVerifiers: verifiers,
		cacheLimit:      limit,
	}
}

// CachingHandler implements to/from handle via an LRU cache.
type CachingHandler struct {
	nfs.Handler
	activeHandles   *lru.Cache
	activeVerifiers *lru.Cache
	cacheLimit      int
}

type entry struct {
	f billy.Filesystem
	p []string
}

// ToHandle takes a file and represents it with an opaque handle to reference it.
// In stateless nfs (when it's serving a unix fs) this can be the device + inode
// but we can generalize with a stateful local cache of handed out IDs.
func (c *CachingHandler) ToHandle(f billy.Filesystem, path []string) []byte {
	id := uuid.New()
	c.activeHandles.Add(id, entry{f, path})
	b, _ := id.MarshalBinary()
	return b
}

// FromHandle converts from an opaque handle to the file it represents
func (c *CachingHandler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	id, err := uuid.FromBytes(fh)
	if err != nil {
		return nil, []string{}, err
	}

	if cache, ok := c.activeHandles.Get(id); ok {
		f, ok := cache.(entry)
		for _, k := range c.activeHandles.Keys() {
			e, _ := c.activeHandles.Peek(k)
			candidate := e.(entry)
			if hasPrefix(f.p, candidate.p) {
				_, _ = c.activeHandles.Get(k)
			}
		}
		if ok {
			return f.f, f.p, nil
		}
	}
	return nil, []string{}, &nfs.NFSStatusError{NFSStatus: nfs.NFSStatusStale}
}

// HandleLimit exports how many file handles can be safely stored by this cache.
func (c *CachingHandler) HandleLimit() int {
	return c.cacheLimit
}

func hasPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, e := range prefix {
		if path[i] != e {
			return false
		}
	}
	return true
}

type verifier struct {
	handle   []byte
	contents []fs.FileInfo
}

func (c *CachingHandler) VerifierFor(handle []byte, contents []fs.FileInfo) uint64 {
	id := rand.Uint64()
	c.activeVerifiers.Add(id, verifier{handle, contents})
	return id
}

func (c *CachingHandler) DataForVerifier(handle []byte, id uint64) []fs.FileInfo {
	if cache, ok := c.activeVerifiers.Get(id); ok {
		f, ok := cache.(verifier)
		if ok && bytes.Equal(handle, f.handle) {
			return f.contents
		}
	}
	return nil
}
