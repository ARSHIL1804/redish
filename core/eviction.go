package core

import (
	"math/rand"
	"redish/config"
	"strings"
	"time"
)

const (
	lfuCounterMask uint32 = 0xff
	lfuTimeMask    uint32 = 0xffff
	lruClockMask   uint32 = 0xffffff
)

func evictIfNeededLocked() bool {
	if config.MaxKeys <= 0 || len(store) <= config.MaxKeys {
		return true
	}

	for len(store) > config.MaxKeys {
		sampleSize := config.EvictionSampleSize
		if sampleSize <= 0 {
			sampleSize = 1
		}

		var candidateKey string
		var candidate *Obj
		sampled := 0
		for key, obj := range store {
			if obj.ExpiresAt <= 0 {
				continue
			}
			sampled++
			if candidate == nil || isWorseCandidate(obj, candidate) {
				candidateKey = key
				candidate = obj
			}
			if sampled >= sampleSize {
				break
			}
		}

		if candidate == nil {
			return false
		}
		delete(store, candidateKey)
		_ = appendAOF(aofRecord{Op: "del", Key: candidateKey})
	}
	return true
}

func isWorseCandidate(candidate, current *Obj) bool {
	switch strings.ToLower(config.EvictionPolicy) {
	case "lru":
		return olderLRUClock(candidate.PolicyData, current.PolicyData)
	case "lfu", "":
		candidateFrequency := effectiveLFUFrequency(candidate)
		currentFrequency := effectiveLFUFrequency(current)
		if candidateFrequency != currentFrequency {
			return candidateFrequency < currentFrequency
		}
		return false
	default:
		return effectiveLFUFrequency(candidate) < effectiveLFUFrequency(current)
	}
}

func isLRUPolicy() bool {
	return strings.EqualFold(config.EvictionPolicy, "lru")
}

func currentLFUTime() int64 {
	return (time.Now().Unix() / 60) & int64(lfuTimeMask)
}

func currentLRUClock() uint32 {
	return uint32(time.Now().Unix()) & lruClockMask
}

func olderLRUClock(candidate, current uint32) bool {
	return ((current - candidate) & lruClockMask) < (lruClockMask / 2)
}

func lfuInitialCounter() uint8 {
	value := config.LFUInitialCounter
	if value < 0 {
		value = 0
	}
	if value > 255 {
		value = 255
	}
	return uint8(value)
}

func newLFUData() uint32 {
	return uint32(lfuInitialCounter()) | (uint32(currentLFUTime())&lfuTimeMask)<<8
}

func lfuCounter(obj *Obj) uint8 {
	return uint8(obj.PolicyData & lfuCounterMask)
}

func lfuTime(obj *Obj) int64 {
	return int64((obj.PolicyData >> 8) & lfuTimeMask)
}

func setLFUData(obj *Obj, counter uint8, timestamp int64) {
	obj.PolicyData = uint32(counter) | (uint32(timestamp)&lfuTimeMask)<<8
}

func lfuDecay(obj *Obj) {
	decayMinutes := config.LFUDecayMinutes
	if decayMinutes <= 0 || lfuTime(obj) <= 0 {
		return
	}

	periods := lfuElapsedMinutes(obj) / int64(decayMinutes)
	if periods <= 0 {
		return
	}
	counter := lfuCounter(obj)
	if periods >= int64(counter) {
		counter = 0
	} else {
		counter -= uint8(periods)
	}
	setLFUData(obj, counter, lfuTime(obj)+periods*int64(decayMinutes))
}

func lfuIncrement(obj *Obj) {
	counter := lfuCounter(obj)
	if counter >= 255 {
		return
	}

	logFactor := config.LFULogFactor
	if logFactor < 1 {
		logFactor = 1
	}
	probability := 1.0 / (float64(counter)*float64(logFactor) + 1.0)
	if rand.Float64() < probability {
		setLFUData(obj, counter+1, lfuTime(obj))
	}
}

func effectiveLFUFrequency(obj *Obj) uint8 {
	frequency := lfuCounter(obj)
	decayMinutes := config.LFUDecayMinutes
	if decayMinutes <= 0 || lfuTime(obj) <= 0 {
		return frequency
	}
	periods := lfuElapsedMinutes(obj) / int64(decayMinutes)
	if periods >= int64(frequency) {
		return 0
	}
	return frequency - uint8(periods)
}

func lfuElapsedMinutes(obj *Obj) int64 {
	return int64((uint32(currentLFUTime()) - uint32(lfuTime(obj))) & lfuTimeMask)
}
