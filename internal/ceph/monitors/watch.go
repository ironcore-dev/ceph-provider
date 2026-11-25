// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-logr/logr"
)

// settleDuration waits out the burst of writes kubelet makes when it swaps
// the mounted ConfigMap's ..data symlink.
const settleDuration = 100 * time.Millisecond

// ErrMonitorListChanged is returned by Watcher.Start when the mounted
// ConfigMap parses to a CSV other than Baseline. The process exits so the
// next start connects with the new list.
var ErrMonitorListChanged = errors.New("ceph monitor list changed")

// Watcher reads a ConfigMap mounted as files and watches that directory.
// Each ConfigMap key is a file name in Dir. Kubelet serves those files
// through a ..data symlink; reads follow the symlink.
type Watcher struct {
	Dir       string
	Key       string
	ClusterID string
	Log       logr.Logger
	// Baseline is the CSV already passed to ConnectToRados.
	Baseline string
	// added is closed after Dir is being watched. Tests wait on it.
	added chan struct{}
}

// Bootstrap reads the mounted files once and returns a normalized monitor CSV.
func (w *Watcher) Bootstrap(ctx context.Context) (string, error) {
	if err := w.validate(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return readMounted(w.Dir, w.Key, w.ClusterID)
}

// Start watches Dir until ctx is done or the parsed CSV differs from Baseline.
// A missing file or a later parse error keeps Baseline in place and does not
// return an error.
func (w *Watcher) Start(ctx context.Context) error {
	if err := w.validate(); err != nil {
		return err
	}
	if w.Log.GetSink() == nil {
		w.Log = logr.Discard()
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create monitor directory watcher: %w", err)
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			w.Log.Error(err, "failed to close monitor directory watcher")
		}
	}()
	if err := watcher.Add(w.Dir); err != nil {
		return fmt.Errorf("watch monitor directory %s: %w", w.Dir, err)
	}
	if w.added != nil {
		close(w.added)
	}

	if csv, err := readMounted(w.Dir, w.Key, w.ClusterID); err == nil && csv != w.Baseline {
		return fmt.Errorf("%w: %s", ErrMonitorListChanged, csv)
	}

	settle := time.NewTimer(0)
	if !settle.Stop() {
		<-settle.C
	}
	defer settle.Stop()
	pending := false

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			pending = true
			if !settle.Stop() {
				select {
				case <-settle.C:
				default:
				}
			}
			settle.Reset(settleDuration)
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			w.Log.Error(err, "monitor directory watch")
		case <-settle.C:
			if !pending {
				continue
			}
			pending = false
			csv, err := readMounted(w.Dir, w.Key, w.ClusterID)
			if err != nil {
				w.Log.Error(err, "failed to parse mounted monitor config", "dir", w.Dir)
				continue
			}
			if csv != w.Baseline {
				w.Log.Info("ceph monitor list changed", "monitors", csv)
				return fmt.Errorf("%w: %s", ErrMonitorListChanged, csv)
			}
		}
	}
}

func (w *Watcher) validate() error {
	if w.Dir == "" {
		return fmt.Errorf("monitor config directory must be set")
	}
	if w.Key == "" {
		return fmt.Errorf("monitor config key must be set")
	}
	return nil
}

func readMounted(dir, key, clusterID string) (string, error) {
	data := make(map[string]string, 2)
	for _, name := range []string{key, rookDataKey} {
		if _, ok := data[name]; ok {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		data[name] = string(raw)
	}
	endpoints, err := ParseConfigMapData(data, key, clusterID)
	if err != nil {
		return "", err
	}
	return Join(endpoints), nil
}
