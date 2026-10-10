package main

import (
	"fmt"
	"strings"

	"github.com/sjzsdu/tongstock/internal/adapter/methodregistryrepo"
	"github.com/sjzsdu/tongstock/internal/adapter/validationrepo"
	"github.com/sjzsdu/tongstock/internal/methodregistry"
	"github.com/sjzsdu/tongstock/internal/methodseed"
	"github.com/sjzsdu/tongstock/internal/paradigm"
	"github.com/spf13/cobra"
)

var (
	seedSnapshot string
	seedUniverse string
	seedStart    string
	seedEnd      string
	seedMaxCodes int
	seedKeys     []string
	seedSplit    string
	seedTrials   int
)

var methodLibrarySeedCmd = &cobra.Command{
	Use:   "seed",
	Short: "载入内置示例方法：真实回测验证后注册到方法库",
	Long: `把内置示例方法（均线多头排列 / MACD 多头 / 量价突破）逐个编译，
在已冻结的真实日线快照上做全股票池回测，按验证工厂的真实结论注册到方法库。
未过门槛的方法会被如实标注为 rejected/candidate，绝不硬编码为 verified。

用法示例：
  tongstock method library seed --snapshot discovery-1786158672652829000 --max-codes 300`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := openValidationStorage()
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.Migrate(); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}

		registryRepo, err := methodregistryrepo.New(store)
		if err != nil {
			return err
		}
		registry, err := methodregistry.New(registryRepo)
		if err != nil {
			return err
		}
		evidenceRepo, err := validationrepo.NewEvidenceRepository(store)
		if err != nil {
			return err
		}

		svc, err := methodseed.NewService(methodseed.Deps{
			Registry:  registry,
			Snapshots: paradigm.NewDatasetSnapshotStore(store),
			Bars:      validationrepo.New(store),
			Benchmark: validationrepo.NewBenchmark(store),
			Evidence:  evidenceRepo,
			Universe:  validationrepo.NewSnapshotUniverse(store),
		})
		if err != nil {
			return err
		}

		result, err := svc.Run(cmd.Context(), methodseed.Options{
			SnapshotID:      seedSnapshot,
			Universe:        splitSeedCodes(seedUniverse),
			DateStart:       seedStart,
			DateEnd:         seedEnd,
			MaxCodes:        seedMaxCodes,
			Keys:            seedKeys,
			SplitType:       seedSplit,
			DiscoveryTrials: seedTrials,
		})
		if err != nil {
			return fmt.Errorf("seed methods failed (fail closed): %w", err)
		}
		return printValidationJSON(result)
	},
}

func splitSeedCodes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func init() {
	methodLibrarySeedCmd.Flags().StringVar(&seedSnapshot, "snapshot", "", "已冻结的真实数据快照 ID（空=用 --universe/--start/--end 新建）")
	methodLibrarySeedCmd.Flags().StringVar(&seedUniverse, "universe", "", "股票代码列表（逗号分隔）；空=从快照解析")
	methodLibrarySeedCmd.Flags().StringVar(&seedStart, "start", "", "新建快照的起始日 YYYY-MM-DD")
	methodLibrarySeedCmd.Flags().StringVar(&seedEnd, "end", "", "新建快照的截止日 YYYY-MM-DD")
	methodLibrarySeedCmd.Flags().IntVar(&seedMaxCodes, "max-codes", 300, "从快照解析股票池时的上限")
	methodLibrarySeedCmd.Flags().StringSliceVar(&seedKeys, "keys", nil, "只载入指定 key 的种子方法（可重复）；空=全部")
	methodLibrarySeedCmd.Flags().StringVar(&seedSplit, "split", "", "切分类型: fixed / walk_forward（空=walk_forward）")
	methodLibrarySeedCmd.Flags().IntVar(&seedTrials, "trials", 1, "发现阶段尝试次数（多重检验惩罚）")
	methodLibraryCmd.AddCommand(methodLibrarySeedCmd)
}
