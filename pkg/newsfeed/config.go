package newsfeed

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// topic 语料扩展与展示参数的配置。
//
// 两组开关都遵循同一条规则：默认值保持既有行为不变（headline=3、
// 置信度地板=0.5、补抓开启但上限保守），环境变量只用于覆盖默认，
// Set* 方法用于程序化覆盖（后调用者生效）。
const (
	// EnvTopicPoolSync 候选股池定向补抓的开关，"0"/"false"/"off" 关闭。
	EnvTopicPoolSync = "TONGSTOCK_TOPIC_POOL_SYNC"
	// EnvTopicPoolSyncLimit 每轮补抓的候选股数量上限 N。
	EnvTopicPoolSyncLimit = "TONGSTOCK_TOPIC_POOL_SYNC_LIMIT"
	// EnvTopicPoolSyncConcurrency 补抓的并发上限。
	EnvTopicPoolSyncConcurrency = "TONGSTOCK_TOPIC_POOL_SYNC_CONCURRENCY"
	// EnvTopicHeadlineLimit 每股代表新闻条数（现值 3）。
	EnvTopicHeadlineLimit = "TONGSTOCK_TOPIC_HEADLINE_LIMIT"
	// EnvTopicConfidenceFloor 榜单关联置信度地板（现值 0.5）。
	EnvTopicConfidenceFloor = "TONGSTOCK_TOPIC_CONFIDENCE_FLOOR"
)

// 补抓参数的保守默认值。上限 N=5、并发 3、单股总预算 6s、单源预算 2s：
// 最坏情况下单轮额外耗时 ≈ ceil(5/3)*6s ≈ 12s，叠加在 GlobalSync 之上
// 仍有界（后台同步的 60s 预算内，超预算会由 ctx 取消自然中止）。
const (
	// DefaultStockPoolSyncLimit 每轮补抓的候选股数量上限。
	DefaultStockPoolSyncLimit = 5
	// DefaultStockPoolConcurrency 补抓并发上限。
	DefaultStockPoolConcurrency = 3
	// DefaultStockPoolPerStockTimeout 单只股票（跨全部源）的总时间预算。
	DefaultStockPoolPerStockTimeout = 6 * time.Second
	// DefaultStockPoolPerSourceTimeout 单只股票在单个数据源上的时间预算。
	DefaultStockPoolPerSourceTimeout = 2 * time.Second
)

// StockPoolSyncConfig 控制 GlobalSync 后的候选股池 FetchByStock 定向补抓。
type StockPoolSyncConfig struct {
	// Enabled 总开关。false 时完全跳过补抓，回到旧行为。
	Enabled bool
	// Limit 每轮补抓的候选股数量上限 N；<=0 时用 DefaultStockPoolSyncLimit。
	Limit int
	// Concurrency 同时补抓的股票数；<=0 时用 DefaultStockPoolConcurrency。
	Concurrency int
	// PerStockTimeout 单只股票的总时间预算；<=0 时用默认值。
	PerStockTimeout time.Duration
	// PerSourceTimeout 单只股票在单个源上的时间预算；<=0 时用默认值。
	PerSourceTimeout time.Duration
}

// DefaultStockPoolSyncConfig 返回默认补抓配置（开启、上限保守）。
func DefaultStockPoolSyncConfig() StockPoolSyncConfig {
	return StockPoolSyncConfig{
		Enabled:          true,
		Limit:            DefaultStockPoolSyncLimit,
		Concurrency:      DefaultStockPoolConcurrency,
		PerStockTimeout:  DefaultStockPoolPerStockTimeout,
		PerSourceTimeout: DefaultStockPoolPerSourceTimeout,
	}
}

// normalize 补齐零值字段，保证并发与超时始终有界。
func (c StockPoolSyncConfig) normalize() StockPoolSyncConfig {
	if c.Limit <= 0 {
		c.Limit = DefaultStockPoolSyncLimit
	}
	if c.Concurrency <= 0 {
		c.Concurrency = DefaultStockPoolConcurrency
	}
	if c.PerStockTimeout <= 0 {
		c.PerStockTimeout = DefaultStockPoolPerStockTimeout
	}
	if c.PerSourceTimeout <= 0 {
		c.PerSourceTimeout = DefaultStockPoolPerSourceTimeout
	}
	if c.PerSourceTimeout > c.PerStockTimeout {
		c.PerSourceTimeout = c.PerStockTimeout
	}
	return c
}

// stockPoolSyncConfigFromEnv 在默认配置之上应用环境变量覆盖。
func stockPoolSyncConfigFromEnv(base StockPoolSyncConfig) StockPoolSyncConfig {
	if v, ok := os.LookupEnv(EnvTopicPoolSync); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "0", "false", "off", "no":
			base.Enabled = false
		case "1", "true", "on", "yes":
			base.Enabled = true
		}
	}
	if n, ok := intEnv(EnvTopicPoolSyncLimit); ok {
		base.Limit = n
	}
	if n, ok := intEnv(EnvTopicPoolSyncConcurrency); ok {
		base.Concurrency = n
	}
	return base.normalize()
}

// topicTunablesFromEnv 读取 headline 条数与置信度地板的环境变量覆盖。
func topicTunablesFromEnv(headline int, floor float64) (int, float64) {
	if n, ok := intEnv(EnvTopicHeadlineLimit); ok && n > 0 {
		headline = n
	}
	if f, ok := floatEnv(EnvTopicConfidenceFloor); ok && f >= 0 && f <= 1 {
		floor = f
	}
	return headline, floor
}

func intEnv(key string) (int, bool) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	return n, true
}

func floatEnv(key string) (float64, bool) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
