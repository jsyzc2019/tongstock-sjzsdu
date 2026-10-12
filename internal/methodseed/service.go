package methodseed

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sjzsdu/tongstock/internal/methodregistry"
	"github.com/sjzsdu/tongstock/internal/methods"
	"github.com/sjzsdu/tongstock/internal/paradigm"
	"github.com/sjzsdu/tongstock/internal/adapter/validationrepo"
	"github.com/sjzsdu/tongstock/internal/validation"
)

// KlineTypeDaily 与 validationrepo.KlineTypeDaily 一致（通达信 A 股日线）。
const KlineTypeDaily uint8 = 9

// SnapshotStore 负责创建/读取不可变冻结快照。实现见 paradigm.DatasetSnapshotStore。
type SnapshotStore interface {
	CreateKlineSnapshot(snapshot *paradigm.DatasetSnapshot, ktype uint8) error
	GetByID(id string) (*paradigm.DatasetSnapshot, error)
	VerifyContent(snapshotID string) error
}

// EvidenceSink 持久化验证制品（可选）。缺失时只注册方法、不落证据制品。
type EvidenceSink interface {
	Save(ctx context.Context, bundle *validation.EvidenceBundle) error
}

// UniverseResolver 从冻结快照解析真实代码列表（可选）。
type UniverseResolver interface {
	ResolveUniverse(ctx context.Context, snapshotID string, minBars, maxCodes int) ([]string, int, error)
}

// Deps 是内置示例方法载入的装配依赖。
type Deps struct {
	Registry  *methodregistry.Registry
	Snapshots SnapshotStore
	Bars      validation.BarProvider
	Benchmark validation.BenchmarkProvider
	Evidence  EvidenceSink
	Universe  UniverseResolver
}

// Service 编排「编译 → 真实回测 → 注册」。
type Service struct {
	deps Deps
	now  func() time.Time
}

// NewService 构造内置示例方法服务。
func NewService(deps Deps) (*Service, error) {
	if deps.Registry == nil {
		return nil, fmt.Errorf("methodseed requires a method registry")
	}
	if deps.Snapshots == nil || deps.Bars == nil {
		return nil, fmt.Errorf("methodseed requires snapshot store and bar provider")
	}
	return &Service{deps: deps, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Run 载入内置示例方法：逐个编译、在冻结真实数据上做全池回测、按真实结果注册到方法库。
// 任何方法是否可信完全由验证工厂决定；未过门槛的方法如实标注，绝不硬编码为 verified。
func (s *Service) Run(ctx context.Context, opts Options) (*Result, error) {
	snapshot, universe, skippedUniverse, err := s.resolveInput(ctx, opts)
	if err != nil {
		return nil, err
	}

	seeds := filterSeeds(opts.Keys)
	res := &Result{
		SnapshotID:   snapshot.ID,
		UniverseSize: len(universe) + skippedUniverse,
		DateStart:    snapshot.DateRange.Start,
		DateEnd:      snapshot.DateRange.End,
		Outcomes:     make([]Outcome, 0, len(seeds)),
		FinishedAt:   s.now(),
	}
	if len(seeds) == 0 {
		return res, fmt.Errorf("no seed method matched keys %v", opts.Keys)
	}

	for _, seed := range seeds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		outcome := s.loadOne(ctx, seed, snapshot, universe, opts)
		res.Outcomes = append(res.Outcomes, outcome)
		if outcome.MethodID != "" {
			res.Registered++
		}
		if outcome.Status == string(methodregistry.StatusVerified) {
			res.Verified++
		}
	}
	res.FinishedAt = s.now()
	return res, nil
}

// resolveInput 确定数据源与验证股票池。
// 项目红线：数据永远读活表真实全量，不冻结/不使用快照。SnapshotID 参数仅作
// 兼容保留并被忽略；数据源身份固定为 validationrepo.LiveSnapshotID。
func (s *Service) resolveInput(ctx context.Context, opts Options) (*paradigm.DatasetSnapshot, []string, int, error) {
	snapshot, err := s.deps.Snapshots.GetByID(validationrepo.LiveSnapshotID)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("resolve live data source: %w", err)
	}
	universe, skipped, err := s.resolveUniverse(ctx, "", opts)
	if err != nil {
		return nil, nil, 0, err
	}
	return snapshot, universe, skipped, nil
}

func (s *Service) resolveUniverse(ctx context.Context, snapshotID string, opts Options) ([]string, int, error) {
	if len(opts.Universe) > 0 {
		return dedupeCodes(opts.Universe), 0, nil
	}
	if s.deps.Universe == nil {
		return nil, 0, fmt.Errorf("no explicit universe and no universe resolver configured")
	}
	// MaxCodes<=0 表示不设上限：使用快照内全部合格股票。
	codes, skipped, err := s.deps.Universe.ResolveUniverse(ctx, snapshotID, 30, opts.MaxCodes)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve snapshot universe: %w", err)
	}
	if len(codes) == 0 {
		return nil, 0, fmt.Errorf("snapshot %s has no code with enough real bars", snapshotID)
	}
	return codes, skipped, nil
}

// loadOne 编译、验证并注册单个种子方法。
func (s *Service) loadOne(ctx context.Context, seed methods.SeedCandidate, snapshot *paradigm.DatasetSnapshot, universe []string, opts Options) Outcome {
	out := Outcome{Key: seed.Key}
	if seed.Candidate == nil {
		out.Status = "invalid"
		out.Error = "seed candidate is nil"
		return out
	}
	out.Name = seed.Candidate.Name

	compiled, diags, err := methods.Compile(seed.Candidate)
	if err != nil {
		out.Status = "invalid"
		out.Error = fmt.Sprintf("compile: %v", err)
		return out
	}
	if compiled == nil || !compiled.IsExecutable() {
		out.Status = "invalid"
		out.Error = "compiled method is not executable: " + diagnosticsSummary(diags)
		return out
	}
	out.MethodHash = compiled.ContentHash

	// 同 family + methodHash 已登记过就跳过：种子方法内容是确定性的，重复
	// 载入只会原样重跑回测并再写一条 register 审计事件，污染证据链。
	if existing, err := s.deps.Registry.Cards(ctx, methodregistry.Query{FamilyID: "seed-" + seed.Key, Limit: 50}); err == nil {
		for _, card := range existing {
			if card.VariantID == compiled.ContentHash {
				out.Status = "skipped_registered"
				out.MethodID = card.ID
				out.Error = "同内容方法已登记过（family+hash 相同），跳过重复载入"
				return out
			}
		}
	}

	job := validation.ValidationJob{
		MethodHash:      compiled.ContentHash,
		MethodName:      compiled.Name,
		SnapshotID:      snapshot.ID,
		Universe:        universe,
		DateStart:       snapshot.DateRange.Start,
		DateEnd:         snapshot.DateRange.End,
		SplitType:       opts.SplitType,
		DiscoveryTrials: opts.DiscoveryTrials,
	}
	factory, err := validation.NewFactory(validation.FactoryDeps{
		Method: compiled, Bars: s.deps.Bars, Benchmark: s.deps.Benchmark,
	})
	if err != nil {
		out.Status = "failed"
		out.Error = err.Error()
		return out
	}
	bundle, err := factory.Run(ctx, job)
	if err != nil {
		out.Status = "failed"
		out.Error = fmt.Sprintf("validation: %v", err)
		return out
	}
	if s.deps.Evidence != nil {
		if saveErr := s.deps.Evidence.Save(ctx, bundle); saveErr != nil {
			out.Status = "failed"
			out.Error = fmt.Sprintf("persist evidence: %v", saveErr)
			return out
		}
	}
	out.Confidence = string(bundle.Confidence)
	out.Passable = bundle.Passable
	out.OOSTrades = bundle.OosStats.TotalTrades
	out.OOSReturn = bundle.OosStats.TotalReturn
	out.OOSMaxDrawdown = bundle.OosStats.MaxDrawdown
	out.ResultHash = bundle.ResultHash
	for _, b := range bundle.Blockers {
		out.Blockers = append(out.Blockers, b.Code)
	}

	m, err := s.deps.Registry.Register(ctx, methodregistry.Registration{
		FamilyID:         "seed-" + seed.Key,
		VariantID:        compiled.ContentHash,
		Name:             "[内置] " + compiled.Name,
		ValidationJobID:  bundle.JobHash,
		Market:           "A",
		TriggerFrequency: "daily",
		EntrySummary:     seed.Rationale,
		ExitSummary:      exitSummary(compiled),
		Method:           compiled,
		Evidence:         methodregistry.ValidationEvidence{Bundle: bundle},
	})
	if err != nil {
		out.Status = "failed"
		out.Error = fmt.Sprintf("register: %v", err)
		return out
	}
	out.MethodID = m.ID
	out.Status = string(m.Status)
	return out
}

func filterSeeds(keys []string) []methods.SeedCandidate {
	all := methods.SeedCandidates()
	if len(keys) == 0 {
		return all
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[strings.TrimSpace(k)] = true
	}
	out := make([]methods.SeedCandidate, 0, len(all))
	for _, s := range all {
		if want[s.Key] {
			out = append(out, s)
		}
	}
	return out
}

func dedupeCodes(codes []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func diagnosticsSummary(diags []methods.Diagnostic) string {
	parts := make([]string, 0, len(diags))
	for _, d := range diags {
		if d.Level == "error" || d.Level == "ambiguous" {
			parts = append(parts, d.Code)
		}
	}
	if len(parts) == 0 {
		return "no diagnostics"
	}
	return strings.Join(parts, ",")
}

func exitSummary(m *methods.CompiledMethod) string {
	return methods.ExitSummary(m)
}
