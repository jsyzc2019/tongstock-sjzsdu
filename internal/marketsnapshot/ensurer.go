package marketsnapshot

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// FeatureEnsurer 保证「给定市场快照 → 一个覆盖目标特征集的 ready 特征快照」。
// 库内只有升级前物化的旧特征快照（例如 gap_pct/return1 进入 DefaultDslFeatures
// 之前的 22 特征集）时就地物化一个新的，避免选股端因特征缺失把方法整批打成
// insufficient_data（发现端能验证、选股端却筛不出的「挖出来也用不了」卡点）。
//
// selection.Engine 通过同名方法 EnsureFeatureSnapshot 注入；onboarding 的特征
// 步骤复用同一个实现，保证两条链路口径一致。
type FeatureEnsurer struct {
	Builder   *Builder
	Snapshots Repository
	Features  FeatureEngine
	// Required 为空时使用 LatestSelectionFeatures()。
	Required []FeatureSpec
	// Now 为空时使用 time.Now（保证新快照 built_at 总是新鲜的，不沿用
	// Builder 构造时刻）。
	Now func() time.Time
}

// Ensure 复用或物化一个覆盖 required 的特征快照。
// 返回：快照、是否复用了已有快照、被跳过的旧快照说明（id + 缺失特征，供
// 调用方把「为什么重建」写进步骤明细/日志）、错误。
func (e *FeatureEnsurer) Ensure(ms *MarketSnapshot) (*FeatureSnapshot, bool, []string, error) {
	if e == nil || e.Builder == nil || e.Snapshots == nil || e.Features == nil {
		return nil, false, nil, fmt.Errorf("feature ensurer not configured")
	}
	if ms == nil || ms.Status != StatusReady {
		return nil, false, nil, fmt.Errorf("market snapshot not ready for feature materialization")
	}
	required := e.Required
	if len(required) == 0 {
		required = LatestSelectionFeatures()
	}
	requiredNames := FeatureNames(required)

	var skipped []string
	if existing, err := e.Snapshots.ListFeatureSnapshots(ms.ID); err == nil {
		for _, item := range existing {
			if item == nil || item.Status != StatusReady || !item.LeakChecked {
				continue
			}
			// 特征集不满足要求的旧快照一律不复用——哪怕它更新。
			if missing := item.MissingFeatures(requiredNames); len(missing) > 0 {
				skipped = append(skipped, fmt.Sprintf("%s 缺少 %s", item.ID, strings.Join(missing, ",")))
				continue
			}
			full, loadErr := e.Snapshots.LoadFeatureSnapshot(item.ID, true)
			if loadErr == nil && full != nil {
				return full, true, skipped, nil
			}
		}
	}

	b := *e.Builder
	if e.Now != nil {
		b.Now = e.Now()
	} else {
		b.Now = time.Now()
	}
	fs, err := b.BuildFeatureSnapshot(ms, required, e.Features)
	if err != nil {
		return nil, false, skipped, fmt.Errorf("build feature snapshot: %w", err)
	}
	if err := e.Snapshots.SaveFeatureSnapshot(fs); err != nil {
		return nil, false, skipped, fmt.Errorf("save feature snapshot: %w", err)
	}
	return fs, false, skipped, nil
}

// EnsureFeatureSnapshot 满足 selection.FeatureEnsurer 接口：选股引擎在未显式
// 指定 feature_snapshot_id 时调用，保证方法依赖的内建特征（gap_pct 等）被
// 物化后再执行规则。
func (e *FeatureEnsurer) EnsureFeatureSnapshot(_ context.Context, ms *MarketSnapshot) (*FeatureSnapshot, error) {
	fs, _, _, err := e.Ensure(ms)
	return fs, err
}
