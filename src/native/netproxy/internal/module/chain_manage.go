package module

import (
	"context"
	"errors"
	"os"
	"slices"

	moduleconfig "github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/config"
	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/paths"
	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/service"
)

// ChainStatus 是 CLI 与面板共用的链路状态视图。
type ChainStatus struct {
	Management ChainSelection `json:"management"`
	Chains     []ChainSummary `json:"chains"`
	Fallback   string         `json:"fallback_chain"`
	MinHops    int            `json:"min_hops"`
	Enabled    bool           `json:"enabled"`
	ProxyOn    bool           `json:"proxy_enabled"`
}

// ChainSummary 是单条链路的精简描述。
type ChainSummary struct {
	Name     string   `json:"name"`
	Enabled  bool     `json:"enabled"`
	Priority int      `json:"priority"`
	Hops     []string `json:"hops"`
	MinHops  int      `json:"min_hops"`
	SSID     []string `json:"ssid,omitempty"`
	MCC      []string `json:"mcc,omitempty"`
	MNC      []string `json:"mnc,omitempty"`
}

// ChainStatus 读取链路配置与最近一次解析结果。
func ReadChainStatus(ctx context.Context, options Options) (ChainStatus, error) {
	chains, err := LoadChains(options)
	if err != nil {
		return ChainStatus{}, err
	}
	module, err := moduleconfig.LoadModule(options.ModuleConfig)
	if err != nil {
		return ChainStatus{}, err
	}
	state, err := ReadChainState(chainStatePath(options))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ChainStatus{}, err
	}
	return ChainStatus{
		Management: state,
		Chains:     summarizeChains(chains),
		Fallback:   chains.FallbackChain,
		MinHops:    chains.MinHops,
		Enabled:    chains.Enabled,
		ProxyOn:    module.Proxy.Enabled,
	}, nil
}

// ListChains 返回全部链路摘要。
func ListChains(ctx context.Context, options Options) ([]ChainSummary, error) {
	chains, err := LoadChains(options)
	if err != nil {
		return nil, err
	}
	return summarizeChains(chains), nil
}

func summarizeChains(chains moduleconfig.Chains) []ChainSummary {
	result := make([]ChainSummary, 0, len(chains.Chains))
	for _, chain := range chains.Chains {
		result = append(result, ChainSummary{
			Name:     chain.Name,
			Enabled:  chain.Enabled,
			Priority: chain.Priority,
			Hops:     slices.Clone(chain.Hops),
			MinHops:  effectiveMinHops(chains, chain),
			SSID:     slices.Clone(chain.Match.SSID),
			MCC:      slices.Clone(chain.Match.MCC),
			MNC:      slices.Clone(chain.Match.MNC),
		})
	}
	return result
}

// SelectChain 手动切换链路并立即生效；名称为 auto 时恢复自动选择。
//
// 切换通过 clash_mode 完成，不需要重启内核；旧连接按新链路重新建立。
func SelectChain(ctx context.Context, options Options, name string) (ChainStatus, error) {
	chains, err := LoadChains(options)
	if err != nil {
		return ChainStatus{}, err
	}
	if !chains.Enabled {
		return ChainStatus{}, &service.Error{Code: "chain.disabled", Message: "链路管理总开关未开启"}
	}
	mode, err := chainModeForSelection(ctx, options, chains, name)
	if err != nil {
		return ChainStatus{}, err
	}
	if err := service.SetMode(ctx, networkControlOptions(options), mode); err != nil {
		return ChainStatus{}, err
	}
	// 已有长连接不会自动改道，主动断开让新规则立即生效。
	if err := service.CloseAllConnections(ctx, networkControlOptions(options)); err != nil {
		return ChainStatus{}, err
	}
	state, err := ReadChainStatus(ctx, options)
	if err != nil {
		return ChainStatus{}, err
	}
	state.Management.Chain = name
	state.Management.Active = name != "auto"
	return state, nil
}

// chainModeForSelection 把链路名翻译为 clash_mode。
func chainModeForSelection(ctx context.Context, options Options, chains moduleconfig.Chains, name string) (string, error) {
	if name == "auto" {
		return chainsDefaultMode(options)
	}
	for _, chain := range chains.Chains {
		if chain.Name != name {
			continue
		}
		if !chain.Enabled {
			return "", &service.Error{Code: "chain.disabled", Message: "链路已停用: " + name}
		}
		return chainModeName(name), nil
	}
	return "", &service.Error{Code: "chain.missing", Message: "链路不存在: " + name}
}

// chainsDefaultMode 返回自动模式对应的默认模式名。
//
// 自动模式下不应停留在某条 chain_ 模式上，而是回到主配置的默认模式。
func chainsDefaultMode(options Options) (string, error) {
	modes, err := moduleconfig.LoadModes(paths.SingBoxConfig(options.SingBoxDir))
	if err != nil {
		return "", err
	}
	return modes.Mode, nil
}

// SetChainEnabled 修改单条链路的启用状态并重载配置。
func SetChainEnabled(ctx context.Context, options Options, name string, enabled bool) (ChainStatus, error) {
	return updateChains(ctx, options, func(chains *moduleconfig.Chains) error {
		for index := range chains.Chains {
			if chains.Chains[index].Name == name {
				chains.Chains[index].Enabled = enabled
				return nil
			}
		}
		return &service.Error{Code: "chain.missing", Message: "链路不存在: " + name}
	})
}

// SetChainsEnabled 切换链路管理总开关。
func SetChainsEnabled(ctx context.Context, options Options, enabled bool) (ChainStatus, error) {
	return updateChains(ctx, options, func(chains *moduleconfig.Chains) error {
		chains.Enabled = enabled
		return nil
	})
}

// updateChains 在锁内改写链路配置并重载运行时配置。
func updateChains(ctx context.Context, options Options, mutate func(*moduleconfig.Chains) error) (ChainStatus, error) {
	locked, release, err := lockModeConfig(ctx, options)
	if err != nil {
		return ChainStatus{}, err
	}
	defer release()
	chains, err := LoadChains(locked)
	if err != nil {
		return ChainStatus{}, err
	}
	if err := mutate(&chains); err != nil {
		return ChainStatus{}, err
	}
	if err := chains.Validate(); err != nil {
		return ChainStatus{}, err
	}
	if err := moduleconfig.SaveChains(chainsPath(locked), chains); err != nil {
		return ChainStatus{}, err
	}
	if service.ProcessRunning(locked.SingBoxPath) {
		if err := reloadNetworkConfig(ctx, locked); err != nil {
			return ChainStatus{}, err
		}
	}
	return ReadChainStatus(ctx, locked)
}
