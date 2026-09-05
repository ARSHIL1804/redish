package config

var (
	Host = "0.0.0.0"
	Port = 7379
	MaxClients = 2000
	CronFrequency = 1
	ExpireSampleSize = 20
	MaxKeys = 1000
	EvictionPolicy = "lfu"
	EvictionSampleSize = 5
	LFULogFactor = 10
	LFUDecayMinutes = 1
	LFUInitialCounter = 5
	AOFPath = "appendonly.aof"
	AOFRewriteMinSize int64 = 64 * 1024 * 1024
	AOFFsyncIntervalSeconds = 1
)