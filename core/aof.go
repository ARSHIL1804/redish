package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"redish/config"
	"sync"
	"sync/atomic"
	"time"
)

type aofRecord struct {
	Op        string `json:"op"`
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

var aofState struct {
	sync.Mutex
	file      *os.File
	writer    *bufio.Writer
	stop      chan struct{}
	done      chan struct{}
	replaying bool
	path      string
	rewriting int32
}

// OpenAOF replays the append-only file and starts one-second fsyncs.
func OpenAOF(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return err
	}

	aofState.Lock()
	aofState.file = file
	aofState.writer = bufio.NewWriter(file)
	aofState.path = path
	aofState.replaying = true
	aofState.Unlock()

	if err := replayAOF(file); err != nil {
		file.Close()
		return err
	}

	aofState.Lock()
	aofState.replaying = false
	aofState.stop = make(chan struct{})
	aofState.done = make(chan struct{})
	stop := aofState.stop
	done := aofState.done
	aofState.Unlock()

	go func() {
		interval := time.Duration(config.AOFFsyncIntervalSeconds) * time.Second
		if interval <= 0 {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case <-ticker.C:
				_ = syncAOF()
				maybeRewriteAOF()
			case <-stop:
				return
			}
		}
	}()
	return nil
}

// CloseAOF flushes and syncs pending records before closing the file.
func CloseAOF() error {
	aofState.Lock()
	if aofState.file == nil {
		aofState.Unlock()
		return nil
	}
	close(aofState.stop)
	done := aofState.done
	aofState.Unlock()
	<-done

	aofState.Lock()
	defer aofState.Unlock()
	if err := aofState.writer.Flush(); err != nil {
		return err
	}
	if err := aofState.file.Sync(); err != nil {
		return err
	}
	err := aofState.file.Close()
	aofState.file = nil
	aofState.writer = nil
	return err
}

func appendAOF(record aofRecord) error {
	aofState.Lock()
	defer aofState.Unlock()
	if aofState.file == nil || aofState.replaying {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := aofState.writer.Write(append(data, '\n')); err != nil {
		return err
	}
	return aofState.writer.Flush()
}

func syncAOF() error {
	aofState.Lock()
	defer aofState.Unlock()
	if aofState.file == nil {
		return nil
	}
	if err := aofState.writer.Flush(); err != nil {
		return err
	}
	return aofState.file.Sync()
}

func replayAOF(file *os.File) error {
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record aofRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return fmt.Errorf("invalid AOF record: %w", err)
		}
		if err := applyAOFRecord(record); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func applyAOFRecord(record aofRecord) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	switch record.Op {
	case "set":
		if record.ExpiresAt > 0 && record.ExpiresAt <= time.Now().UnixMilli() {
			delete(store, record.Key)
			return nil
		}
		obj := CreateObj(record.Value, -1)
		obj.ExpiresAt = record.ExpiresAt
		if isLRUPolicy() {
			obj.PolicyData = currentLRUClock()
		} else {
			obj.PolicyData = newLFUData()
		}
		store[record.Key] = obj
	case "del":
		delete(store, record.Key)
	case "expire":
		if obj := store[record.Key]; obj != nil {
			obj.ExpiresAt = record.ExpiresAt
		}
	default:
		return fmt.Errorf("unknown AOF operation %q", record.Op)
	}
	return nil
}

func maybeRewriteAOF() {
	if config.AOFRewriteMinSize <= 0 {
		return
	}
	aofState.Lock()
	file := aofState.file
	aofState.Unlock()
	if file == nil {
		return
	}
	info, err := file.Stat()
	if err != nil || info.Size() < int64(config.AOFRewriteMinSize) {
		return
	}
	if !atomic.CompareAndSwapInt32(&aofState.rewriting, 0, 1) {
		return
	}
	go func() {
		defer atomic.StoreInt32(&aofState.rewriting, 0)
		if err := RewriteAOF(); err != nil {
			// A failed rewrite leaves the existing AOF intact.
			return
		}
	}()
}

// RewriteAOF compacts the log to the current live state and atomically swaps it.
// It runs in the background, while the store lock keeps the snapshot consistent.
func RewriteAOF() error {
	aofState.Lock()
	path := aofState.path
	aofState.Unlock()
	if path == "" {
		return nil
	}
	tmpPath := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".rewrite")
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	storeMu.Lock()
	aofState.Lock()
	err = writeSnapshot(tmp)
	if err == nil {
		err = tmp.Sync()
	}
	if err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err == nil {
		err = aofState.file.Close()
	}
	if err == nil {
		err = os.Rename(tmpPath, path)
	}
	if err == nil {
		var file *os.File
		file, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
		if err == nil {
			aofState.file = file
			aofState.writer = bufio.NewWriter(file)
		}
	}
	aofState.Unlock()
	storeMu.Unlock()
	if err != nil {
		_ = os.Remove(tmpPath)
	}
	return err
}

func writeSnapshot(file *os.File) error {
	writer := bufio.NewWriter(file)
	now := time.Now().UnixMilli()
	for key, obj := range store {
		if obj.ExpiresAt > 0 && obj.ExpiresAt <= now {
			continue
		}
		value, ok := obj.Value.(string)
		if !ok {
			continue
		}
		record := aofRecord{Op: "set", Key: key, Value: value, ExpiresAt: obj.ExpiresAt}
		data, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return writer.Flush()
}