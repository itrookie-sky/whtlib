package logx

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const archiveTimeFormat = "20060102T150405.000000000Z"

type archive struct {
	name string
	time time.Time
}

func (w *fileWriter) archivePath(now time.Time) (path string, err error) {
	prefix := strings.TrimSuffix(w.config.File, ".log")
	for range 10 {
		var id [8]byte
		_, err = rand.Read(id[:])
		if err != nil {
			return
		}
		name := prefix + "." + now.UTC().Format(archiveTimeFormat) + "." + hex.EncodeToString(id[:]) + ".log"
		path = filepath.Join(w.config.Path, name)
		_, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			return path, nil
		}
		if statErr != nil {
			return "", statErr
		}
	}
	return "", fmt.Errorf("logx: unable to allocate archive name")
}

func (w *fileWriter) rotateLocked(now time.Time) (err error) {
	if w.file == nil {
		return w.openLocked(now)
	}
	if w.size == 0 {
		w.started = now
		return
	}
	archivePath, err := w.archivePath(now)
	if err != nil {
		return
	}
	err = w.file.Close()
	w.file = nil
	if err != nil {
		return errors.Join(err, w.openLocked(now))
	}
	err = os.Rename(filepath.Join(w.config.Path, w.config.File), archivePath)
	if err != nil {
		return errors.Join(err, w.openLocked(now))
	}
	w.size = 0
	return w.openLocked(now)
}

// Rotate 手动轮转非空活动文件，并清理历史归档。
func (w *fileWriter) Rotate() (err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	now := time.Now()
	err = w.rotateLocked(now)
	if err != nil {
		return
	}
	return w.cleanupLocked(now)
}

func (w *fileWriter) parseArchive(name string) (created time.Time, ok bool) {
	prefix := strings.TrimSuffix(w.config.File, ".log") + "."
	rest, found := strings.CutPrefix(name, prefix)
	if !found {
		return
	}
	rest, found = strings.CutSuffix(rest, ".log")
	if !found || len(rest) != len(archiveTimeFormat)+1+16 || rest[len(archiveTimeFormat)] != '.' {
		return
	}
	id := rest[len(archiveTimeFormat)+1:]
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 8 {
		return
	}
	created, err = time.Parse(archiveTimeFormat, rest[:len(archiveTimeFormat)])
	return created, err == nil
}

func (w *fileWriter) cleanupLocked(now time.Time) (err error) {
	entries, err := os.ReadDir(w.config.Path)
	if err != nil {
		return
	}
	archives := make([]archive, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		created, ok := w.parseArchive(entry.Name())
		if !ok {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			err = errors.Join(err, infoErr)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		archives = append(archives, archive{name: entry.Name(), time: created})
	}
	slices.SortFunc(archives, func(a, b archive) int {
		return cmp.Or(a.time.Compare(b.time), strings.Compare(a.name, b.name))
	})
	remaining := len(archives)
	for _, item := range archives {
		expired := w.config.RotateBackupExpire > 0 && now.Sub(item.time) >= w.config.RotateBackupExpire
		excess := w.config.RotateBackupLimit > 0 && remaining > w.config.RotateBackupLimit
		if !expired && !excess {
			continue
		}
		removeErr := os.Remove(filepath.Join(w.config.Path, item.name))
		if removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			remaining--
			continue
		}
		err = errors.Join(err, fmt.Errorf("logx: remove archive %q: %w", item.name, removeErr))
	}
	return
}

// Cleanup 按保留时间和数量清理当前前缀的归档，不删除活动文件。
func (w *fileWriter) Cleanup() (err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	return w.cleanupLocked(time.Now())
}
