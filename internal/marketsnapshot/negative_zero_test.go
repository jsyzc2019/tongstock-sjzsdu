package marketsnapshot

import "testing"

// TestComputeFeatureContentHashNegativeZero 回归：SQLite REAL 不保留 -0.0 的
// 符号位，落库重载后 -0 变 +0。哈希必须把两者视为同一内容，否则快照永远
// 过不了 selection 引擎的 content hash 校验（fail-closed 报 mismatch）。
// 现场复现：2026-10-13 全部 3 个特征快照因 45 个 -0.0 单元格（002696 等触发
// willr20=-0）集体 mismatch，选股全挂。
func TestComputeFeatureContentHashNegativeZero(t *testing.T) {
	negZero := negZeroF()
	mk := func(zero float64) *FeatureSnapshot {
		return &FeatureSnapshot{
			SnapshotDate:    "2026-10-12",
			PriceAdjustment: "forward",
			FeatureIDs:      []string{"willr20", "bias20"},
			Values: map[string]map[string]float64{
				"002696": {"willr20": zero, "bias20": -0.5},
				"000001": {"willr20": -4.186, "bias20": zero},
			},
		}
	}
	hNeg, err := ComputeFeatureContentHash(mk(negZero))
	if err != nil {
		t.Fatalf("hash neg zero: %v", err)
	}
	hPos, err := ComputeFeatureContentHash(mk(0))
	if err != nil {
		t.Fatalf("hash pos zero: %v", err)
	}
	if hNeg != hPos {
		t.Fatalf("-0 and +0 must hash identically\nneg=%s\npos=%s", hNeg, hPos)
	}
}

// TestComputeFeatureContentHashDeterministicAfterLoad 模拟完整落库往返：
// 值里所有 -0.0 被 SQLite 归一成 +0.0 后，重算哈希必须与构建时一致。
func TestComputeFeatureContentHashDeterministicAfterLoad(t *testing.T) {
	negZero := negZeroF()
	fs := &FeatureSnapshot{
		SnapshotDate:    "2026-10-12",
		PriceAdjustment: "forward",
		FeatureIDs:      []string{"a", "b"},
		Values: map[string]map[string]float64{
			"000001": {"a": negZero, "b": 1.5},
			"600000": {"a": -2.25, "b": negZero},
		},
	}
	before, err := ComputeFeatureContentHash(fs)
	if err != nil {
		t.Fatalf("hash before: %v", err)
	}
	// 模拟 SQLite 往返：-0.0 → +0.0（值相等性不变，符号位丢失）。
	for _, perCode := range fs.Values {
		for f, v := range perCode {
			if v == 0 {
				perCode[f] = 0
			}
		}
	}
	after, err := ComputeFeatureContentHash(fs)
	if err != nil {
		t.Fatalf("hash after: %v", err)
	}
	if before != after {
		t.Fatalf("hash changed after -0 normalization: %s -> %s", before, after)
	}
}

func negZeroF() float64 {
	z := 0.0
	return -z
}
