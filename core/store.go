package core

import (
	"errors"
	"sync"
	"time"
)

var store map[string]*Obj
var storeMu sync.RWMutex

type Obj struct {
	Value      any
	ExpiresAt  int64
	PolicyData uint32
}

func init() {
	store = make(map[string]*Obj)
}

func CreateObj(value any, durationMs int64) *Obj {
	var expiresAt int64 = -1
	if durationMs > 0 {
		expiresAt = time.Now().UnixMilli() + durationMs
	}

	return &Obj {
		Value: value,
		ExpiresAt: expiresAt,
	}
}


func PUT(key string, value any, durationMs int64) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	previous, existed := store[key]
	var obj = CreateObj(value, durationMs)
	if isLRUPolicy() {
		obj.PolicyData = currentLRUClock()
	} else {
		obj.PolicyData = newLFUData()
	}
	store[key] = obj
	if !evictIfNeededLocked() {
		if existed {
			store[key] = previous
		} else {
			delete(store, key)
		}
		return errors.New("out of memory: no evictable volatile keys")
	}
	if current, ok := store[key]; !ok || current != obj {
		return errors.New("out of memory: key was evicted")
	}
	stringValue, ok := value.(string)
	if !ok {
		return errors.New("AOF supports string values only")
	}
	if err := appendAOF(aofRecord{Op: "set", Key: key, Value: stringValue, ExpiresAt: obj.ExpiresAt}); err != nil {
		return err
	}
	return nil
}


func GET(key string) *Obj { 
	storeMu.Lock()
	defer storeMu.Unlock()

	v := store[key]
	if v != nil {
		if v.ExpiresAt > 0 && v.ExpiresAt <= time.Now().UnixMilli() {
			delete(store, key)
			return nil
		}
		if isLRUPolicy() {
			v.PolicyData = currentLRUClock()
		} else {
			lfuDecay(v)
			lfuIncrement(v)
		}
	}
	return v
}


func DEL(key string) bool {
	storeMu.Lock()
	defer storeMu.Unlock()

	if _, ok := store[key]; ok {
		delete(store, key)
		_ = appendAOF(aofRecord{Op: "del", Key: key})
		return true
	}
	return false
}

func EXPIRE(key string, durationMs int64) bool {
	storeMu.Lock()
	defer storeMu.Unlock()

	obj := store[key]
	if obj == nil || (obj.ExpiresAt > 0 && obj.ExpiresAt <= time.Now().UnixMilli()) {
		if obj != nil {
			delete(store, key)
		}
		return false
	}
	obj.ExpiresAt = time.Now().UnixMilli() + durationMs
	_ = appendAOF(aofRecord{Op: "expire", Key: key, ExpiresAt: obj.ExpiresAt})
	return true
}