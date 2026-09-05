package core

import (
	"redish/config"
	"time"
)


var lastCronExecTime time.Time = time.Now()

func expireSample() float32 {

	limit := config.ExpireSampleSize
	if limit <= 0 {
		limit = 1
	}
	var expiredCount int = 0

	for key, obj := range store {
		if obj.ExpiresAt != -1 {
			limit--
			if obj.ExpiresAt <= time.Now().UnixMilli() {
				delete(store, key)
				expiredCount++
			}
		}
		if limit == 0 {
			break
		}
	}

	return float32(expiredCount) / float32(limit)
}

func RunCleanup() {
	var cronFreDuration time.Duration = time.Duration(config.CronFrequency) * time.Second
	if time.Now().After(lastCronExecTime.Add(cronFreDuration)){
		for {
			frac := expireSample()

			if frac < 0.25 {
				break
			}
		}
		lastCronExecTime = time.Now()
	}
}