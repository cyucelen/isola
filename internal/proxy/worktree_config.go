package proxy

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
)

// worktreeListTTL bounds how long a `git worktree list` is trusted, so a
// worktree that switches branches is noticed without listing on every sync.
// A branch the list has not seen yet relists at once.
const worktreeListTTL = 10 * time.Second

// errNoWorktree means no worktree of the project has the branch checked out.
var errNoWorktree = errors.New("no worktree has the branch checked out")

// worktreeConfigs finds the worktree that has a branch checked out and the
// config in it, so per-service settings follow the branch that owns the service
// rather than the main checkout. Listings and configs are cached; a config is
// reloaded when its file's stamp changes, so an edit applies on the next lookup.
type worktreeConfigs struct {
	root string // the main checkout

	mu       sync.Mutex
	trees    map[string]git.Worktree // by branch; nil until listed
	listedAt time.Time
	listErr  string                     // the listing error last logged, so it is logged once
	missing  map[string]time.Time       // branch -> when a listing last lacked it
	configs  map[string]*worktreeConfig // by worktree path
}

type worktreeConfig struct {
	stamp fileStamp
	cfg   *config.Config // the last config that loaded, nil if none has
	err   error          // why the file at stamp did not load
}

func newWorktreeConfigs(root string) *worktreeConfigs {
	return &worktreeConfigs{root: root, missing: map[string]time.Time{}, configs: map[string]*worktreeConfig{}}
}

// lookup returns the worktree that has branch checked out and its config. When
// the worktree's config file fails to load, it returns the last config that did
// (nil if none) together with the error, so a broken edit can keep the previous
// settings. fresh relists worktrees and rereads the config, for callers about to
// act on the worktree (starting a service) rather than polling it.
func (w *worktreeConfigs) lookup(branch string, fresh bool) (git.Worktree, *config.Config, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	tree, err := w.treeLocked(branch, fresh)
	if err != nil {
		return git.Worktree{}, nil, err
	}
	cfg, err := w.configLocked(tree.Path, fresh)
	return tree, cfg, err
}

func (w *worktreeConfigs) treeLocked(branch string, fresh bool) (git.Worktree, error) {
	now := time.Now()
	tree, ok := w.trees[branch]
	stale := w.trees == nil || now.Sub(w.listedAt) > worktreeListTTL
	// A branch no listing has lacked yet (say, a worktree added since the last
	// one) relists at once; one already known to be missing waits for the TTL.
	retry := !ok && now.Sub(w.missing[branch]) > worktreeListTTL
	var listErr error
	if fresh || stale || retry {
		listErr = w.relistLocked(now)
		tree, ok = w.trees[branch]
	}
	if fresh && listErr != nil {
		return git.Worktree{}, listErr // never act on a stale listing
	}
	if !ok {
		if listErr != nil {
			return git.Worktree{}, listErr
		}
		if _, known := w.missing[branch]; !known {
			logging.Info("proxy: no worktree of %s has %s checked out", w.root, branch)
		}
		w.missing[branch] = now
		return git.Worktree{}, fmt.Errorf("%w: %s", errNoWorktree, branch)
	}
	delete(w.missing, branch)
	return tree, nil
}

// relistLocked refreshes the branch -> worktree map. On failure it keeps the
// previous map and waits for the TTL before trying again.
func (w *worktreeConfigs) relistLocked(now time.Time) error {
	w.listedAt = now
	list, err := git.ListWorktrees(w.root)
	if err != nil {
		if err.Error() != w.listErr {
			w.listErr = err.Error()
			logging.Warn("proxy: listing worktrees of %s: %v; using the main checkout's config", w.root, err)
		}
		return err
	}
	w.listErr = ""
	trees := make(map[string]git.Worktree, len(list))
	for _, t := range list {
		if !t.IsBare && t.Branch != "" {
			trees[t.Branch] = t
		}
	}
	w.trees = trees
	// Forget configs of removed worktrees.
	for path := range w.configs {
		listed := false
		for _, t := range trees {
			listed = listed || t.Path == path
		}
		if !listed {
			delete(w.configs, path)
		}
	}
	return nil
}

func (w *worktreeConfigs) configLocked(path string, fresh bool) (*config.Config, error) {
	stamp := stampOf(filepath.Join(path, config.FileName))
	c := w.configs[path]
	if c != nil && !fresh && c.stamp == stamp {
		return c.cfg, c.err
	}
	if c == nil {
		c = &worktreeConfig{}
		w.configs[path] = c
	}
	c.stamp = stamp
	cfg, err := config.Load(path)
	if err != nil {
		// Log a broken edit once, not on every sync; it is retried when the
		// file changes again.
		if c.err == nil || c.err.Error() != err.Error() {
			logging.Warn("proxy: loading config of worktree %s: %v; keeping the previous one", path, err)
		}
		c.err = err
		return c.cfg, err
	}
	c.cfg, c.err = cfg, nil
	return cfg, nil
}
