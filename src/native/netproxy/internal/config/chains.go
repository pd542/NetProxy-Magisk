package config

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// ChainsMode 是链路文件里可选的取值范围，供 UI 与校验共用。
var (
	ChainAppPolicies = []string{"extend", "replace"}
	ChainFallbacks   = []string{"direct", "keep", "chain"}
)

// ChainMatch 描述一条链路的匹配条件。网络类型为空表示不限制类型。
type ChainMatch struct {
	NetworkType string   `json:"network_type,omitempty"`
	SSID        []string `json:"ssid,omitempty"`
	MCC         []string `json:"mcc,omitempty"`
	MNC         []string `json:"mnc,omitempty"`
	ICCID       []string `json:"iccid,omitempty"`
}

// Chain 是一条可命中的代理链路定义。
//
// Hops 依次为 本地入口 → Hops[0] → Hops[1] → … → 出口，
// 每个元素引用节点池中的节点 tag，或字面量 "direct"。
type Chain struct {
	Name     string          `json:"name"`
	Enabled  bool            `json:"enabled"`
	Priority int             `json:"priority"`
	Match    ChainMatch      `json:"match"`
	Hops     []string        `json:"hops"`
	MinHops  int             `json:"min_hops"`
	OnEmpty  string          `json:"on_empty,omitempty"`
	Override *ChainOverrides `json:"override,omitempty"`
}

// ChainOverrides 是单链路对全局设置的局部覆盖。
type ChainOverrides struct {
	DNS     jsontext.Value `json:"dns,omitempty"`
	Routing jsontext.Value `json:"routing,omitempty"`
	Apps    *AppOverride   `json:"apps,omitempty"`
}

// AppOverride 描述分应用策略在链路维度的覆盖方式。
// Mode=extend 时 PackageNames 追加到全局名单，Mode=replace 时完全替换。
type AppOverride struct {
	Mode         string   `json:"mode"`
	PackageNames []string `json:"package_names"`
}

// Chains 是链路配置文件顶层结构。
type Chains struct {
	Version       int     `json:"version"`
	Enabled       bool    `json:"enabled"`
	MinHops       int     `json:"min_hops"`
	FallbackChain string  `json:"fallback_chain"`
	Chains        []Chain `json:"chains"`
}

// DefaultChains 返回启用链路管理时的默认值，与旧行为保持兼容。
func DefaultChains() Chains {
	return Chains{Version: 1, Enabled: false, MinHops: 2, FallbackChain: "direct", Chains: []Chain{}}
}

// LoadChains 读取链路配置；文件缺失时返回默认值而不是错误。
func LoadChains(path string) (Chains, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultChains(), nil
		}
		return Chains{}, err
	}
	return ParseChains(content)
}

// ParseChains 严格解析链路配置，拒绝 null 与未知字段。
func ParseChains(content []byte) (Chains, error) {
	decoder := jsontext.NewDecoder(strings.NewReader(string(content)))
	for {
		token, err := decoder.ReadToken()
		if err != nil {
			break
		}
		if token.Kind() == 'n' {
			return Chains{}, errors.New("链路配置字段不能为 null")
		}
	}
	chains := DefaultChains()
	if err := json.Unmarshal(content, &chains, json.RejectUnknownMembers(true)); err != nil {
		return Chains{}, fmt.Errorf("链路 JSON 配置无效: %w", err)
	}
	return chains, chains.Validate()
}

// Validate 校验链路定义，确保内核构建阶段不必再处理非法输入。
func (chains Chains) Validate() error {
	if chains.Version != 1 {
		return fmt.Errorf("chains.version 只支持 1，当前为 %d", chains.Version)
	}
	if chains.MinHops < 1 || chains.MinHops > 16 {
		return errors.New("chains.min_hops 必须在 1 到 16 之间")
	}
	if chains.FallbackChain == "" {
		return errors.New("chains.fallback_chain 不能为空，最小链路请使用 direct")
	}
	seen := make(map[string]bool, len(chains.Chains))
	for index, chain := range chains.Chains {
		if err := chain.validate(); err != nil {
			return fmt.Errorf("chains[%d]: %w", index, err)
		}
		if seen[chain.Name] {
			return fmt.Errorf("链路名称重复: %s", chain.Name)
		}
		seen[chain.Name] = true
	}
	if chains.FallbackChain != "direct" && !seen[chains.FallbackChain] {
		return fmt.Errorf("chains.fallback_chain 指向不存在的链路: %s", chains.FallbackChain)
	}
	return nil
}

func (chain Chain) validate() error {
	if chain.Name == "" || len(chain.Name) > 48 || !utf8.ValidString(chain.Name) {
		return errors.New("链路名称必须为 1 到 48 字节的 UTF-8 文本")
	}
	if len(chain.Hops) == 0 {
		return errors.New("链路至少需要一个出口节点")
	}
	minHops := chain.MinHops
	if minHops == 0 {
		minHops = 1
	}
	if minHops < 1 || minHops > len(chain.Hops) {
		return fmt.Errorf("min_hops 必须在 1 到 %d 之间", len(chain.Hops))
	}
	for _, hop := range chain.Hops {
		if strings.TrimSpace(hop) == "" {
			return errors.New("链路节点不能为空，直连请写 direct")
		}
	}
	if chain.OnEmpty != "" && !slices.Contains(ChainFallbacks, chain.OnEmpty) {
		return errors.New("on_empty 只能是 direct、keep 或 chain")
	}
	if chain.Override != nil && chain.Override.Apps != nil {
		apps := chain.Override.Apps
		if !slices.Contains(AppOverrideModes(), apps.Mode) {
			return errors.New("override.apps.mode 只能是 extend 或 replace")
		}
		for _, name := range apps.PackageNames {
			if err := ValidatePackageName(name); err != nil {
				return fmt.Errorf("override.apps: %w", err)
			}
		}
	}
	return nil
}

// AppOverrideModes 返回允许的分应用覆盖方式。
func AppOverrideModes() []string { return ChainAppPolicies }

// Match 判断给定网络是否命中该链路。
//
// 优先级：显式 SSID 与运营商条件同时满足才算命中；
// 只写网卡类型时按类型命中；空条件视为通配。
func (chain Chain) MatchNet(networkType, ssid, mcc, mnc, iccid string) bool {
	match := chain.Match
	if match.NetworkType != "" && match.NetworkType != networkType {
		return false
	}
	hasCarrier := len(match.MCC)+len(match.MNC)+len(match.ICCID) > 0
	if len(match.SSID) > 0 && !slices.Contains(match.SSID, ssid) {
		return false
	}
	if hasCarrier {
		switch {
		case len(match.MCC) > 0 && !slices.Contains(match.MCC, mcc):
			return false
		case len(match.MNC) > 0 && !slices.Contains(match.MNC, mnc):
			return false
		case len(match.ICCID) > 0 && !containsPrefix(match.ICCID, iccid):
			return false
		}
	}
	// 只声明了运营商条件却完全没有运营商信息时不算命中，避免误判。
	if hasCarrier && mcc == "" && mnc == "" && iccid == "" {
		return false
	}
	return true
}

// Resolve 在候选中挑选优先级最高且启用、跳数足够的链路。
// 返回空名称表示没有命中，调用方应使用 fallback。
func (chains Chains) Resolve(networkType, ssid, mcc, mnc, iccid string) (Chain, bool) {
	var best *Chain
	for index := range chains.Chains {
		chain := &chains.Chains[index]
		if !chain.Enabled || len(chain.Hops) == 0 || !chain.MatchNet(networkType, ssid, mcc, mnc, iccid) {
			continue
		}
		if best == nil || chain.Priority > best.Priority {
			best = chain
		}
	}
	if best == nil {
		return Chain{}, false
	}
	return *best, true
}

// ValidatePackageName 校验 Android 应用包名，允许服务端下发的占位值。
func ValidatePackageName(name string) error {
	if name == "" || len(name) > 128 {
		return errors.New("包名长度必须在 1 到 128 之间")
	}
	for _, segment := range strings.Split(name, ".") {
		if segment == "" {
			return errors.New("包名不能包含连续或首尾句点: " + name)
		}
	}
	return nil
}

// LockChains 在写入链路文件前获取独占锁。
func LockChains(ctx context.Context, path string) (*Editor, error) {
	return Lock(ctx, path)
}

// containsPrefix 按前缀匹配 ICCID，运营商号段常用于识别已激活的流量卡。
func containsPrefix(values []string, target string) bool {
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.HasPrefix(target, value) {
			return true
		}
	}
	return false
}

// SaveChains 原子写入链路配置，保持 0600 权限避免代理凭据外泄。
func SaveChains(path string, chains Chains) error {
	if err := chains.Validate(); err != nil {
		return err
	}
	content, err := json.Marshal(chains, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(content, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
