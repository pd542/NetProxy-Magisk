package module

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

	moduleconfig "github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/config"
	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/paths"
	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/worker"
)

// 链路出站统一使用 netproxy-chain/ 前缀，避免与原生标签、Provider 命名空间冲突。
const (
	chainTagPrefix  = "netproxy-chain/"
	chainModePrefix = "chain_"
	// DirectTag 是链路中表示“该跳直连”的字面量。
	DirectTag      = "direct"
	maxChainHops   = 16
	chainActionPtr = "route"
)

// NetworkIdentity 是链路匹配使用的网络身份。
type NetworkIdentity struct {
	NetworkType string `json:"network_type,omitempty"`
	SSID        string `json:"ssid,omitempty"`
	MCC         string `json:"mcc,omitempty"`
	MNC         string `json:"mnc,omitempty"`
	ICCID       string `json:"iccid,omitempty"`
}

// ChainSelection 描述一次链路解析结果，用于状态展示与操作日志。
type ChainSelection struct {
	Active   bool            `json:"active"`
	Chain    string          `json:"chain,omitempty"`
	Mode     string          `json:"mode,omitempty"`
	Entry    string          `json:"entry,omitempty"`
	Hops     []string        `json:"hops,omitempty"`
	Original []string        `json:"original,omitempty"`
	Skipped  []string        `json:"skipped,omitempty"`
	Fallback string          `json:"fallback,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Degraded bool            `json:"degraded,omitempty"`
	MinHops  int             `json:"min_hops,omitempty"`
	Disabled bool            `json:"disabled,omitempty"`
	Network  NetworkIdentity `json:"network,omitempty"`
}

// chainPlan 是写入 sing-box 配置所需的全部生成物。
type chainPlan struct {
	selection ChainSelection
	outbounds []jsontext.Value
	rules     []jsontext.Value
}

func chainsPath(options Options) string {
	return paths.New(options.ModuleDir).ChainsConfig()
}

func chainStatePath(options Options) string {
	return paths.New(options.ModuleDir).ChainSelectorState()
}

// LoadChains 读取链路配置，文件缺失时返回默认关闭状态。
func LoadChains(options Options) (moduleconfig.Chains, error) {
	return moduleconfig.LoadChains(chainsPath(options))
}

// readNetworkIdentity 读取当前网络身份，读取失败时保留已采集到的字段。
func readNetworkIdentity(ctx context.Context, options Options) (NetworkIdentity, error) {
	reader := options.NetworkStateReader
	if reader == nil {
		reader = worker.ReadNetworkState
	}
	state, err := reader(ctx)
	identity := NetworkIdentity{
		NetworkType: state.NetworkType,
		SSID:        state.SSID,
		MCC:         state.MCC,
		MNC:         state.MNC,
		ICCID:       state.ICCID,
	}
	if err != nil {
		return identity, err
	}
	return identity, nil
}

// selectChain 解析当前应使用的链路；没有命中时给出回退结果。
func selectChain(chains moduleconfig.Chains, identity NetworkIdentity) (moduleconfig.Chain, ChainSelection) {
	selection := ChainSelection{Network: identity}
	chain, ok := chains.Resolve(identity.NetworkType, identity.SSID, identity.MCC, identity.MNC, identity.ICCID)
	if ok && chain.Enabled && len(chain.Hops) > 0 {
		selection.Active = true
		selection.Chain = chain.Name
		selection.Hops = slices.Clone(chain.Hops)
		selection.Original = slices.Clone(chain.Hops)
		selection.MinHops = effectiveMinHops(chains, chain)
		selection.Mode = chainModeName(chain.Name)
		return chain, selection
	}
	selection.Fallback = chains.FallbackChain
	selection.Reason = "没有链路命中当前网络"
	if chains.FallbackChain == DirectTag {
		selection.Reason += "，走直连"
		return moduleconfig.Chain{}, selection
	}
	for _, candidate := range chains.Chains {
		if candidate.Name != chains.FallbackChain {
			continue
		}
		if !candidate.Enabled || len(candidate.Hops) == 0 {
			selection.Fallback = DirectTag
			selection.Reason += "，回退链路未启用，走直连"
			return moduleconfig.Chain{}, selection
		}
		selection.Active = true
		selection.Chain = candidate.Name
		selection.Hops = slices.Clone(candidate.Hops)
		selection.Original = slices.Clone(candidate.Hops)
		selection.MinHops = effectiveMinHops(chains, candidate)
		selection.Mode = chainModeName(candidate.Name)
		selection.Reason += "，使用回退链路 " + candidate.Name
		return candidate, selection
	}
	selection.Fallback = DirectTag
	selection.Reason += "，回退链路不存在，走直连"
	return moduleconfig.Chain{}, selection
}

func effectiveMinHops(chains moduleconfig.Chains, chain moduleconfig.Chain) int {
	if chain.MinHops > 0 {
		return chain.MinHops
	}
	return chains.MinHops
}

// buildChainPlan 把链路定义翻译成 sing-box 出站与路由规则。
//
// 逐跳克隆节点原始 JSON 并写入 detour 指向下一跳，因此链路
// 本地入口 → hop0 → hop1 → … → 出口 由内核原生串联完成。
// 失效跳按配置跳过，剩余跳数不足 min_hops 时整链回退。
func buildChainPlan(chains moduleconfig.Chains, chain moduleconfig.Chain, selection ChainSelection, nodes map[string]jsontext.Value) (chainPlan, error) {
	plan := chainPlan{selection: selection}
	if !selection.Active {
		return plan, nil
	}
	minHops := effectiveMinHops(chains, chain)
	hops := make([]string, 0, len(chain.Hops))
	skipped := make([]string, 0)
	for _, hop := range chain.Hops {
		if hop == DirectTag {
			hops = append(hops, hop)
			continue
		}
		if _, ok := nodes[hop]; ok {
			hops = append(hops, hop)
			continue
		}
		skipped = append(skipped, hop)
	}
	if len(hops) > maxChainHops {
		return plan, fmt.Errorf("链路 %s 跳数超过上限 %d", chain.Name, maxChainHops)
	}
	if len(hops) < minHops {
		plan.selection = fallbackSelection(chains, selection, skipped,
			fmt.Sprintf("可用节点 %d 个，少于 min_hops=%d", len(hops), minHops))
		return plan, nil
	}
	selection.Skipped = skipped
	selection.Hops = slices.Clone(hops)
	selection.Degraded = len(skipped) > 0
	selection.MinHops = minHops

	outbounds, entry, err := chainOutbounds(chain.Name, hops, nodes)
	if err != nil {
		return plan, err
	}
	rules, err := chainRules(selection.Mode, entry)
	if err != nil {
		return plan, err
	}
	selection.Entry = entry
	plan.selection = selection
	plan.outbounds = outbounds
	plan.rules = rules
	return plan, nil
}

func fallbackSelection(chains moduleconfig.Chains, selection ChainSelection, skipped []string, reason string) ChainSelection {
	selection.Active = false
	selection.Skipped = skipped
	selection.Degraded = len(skipped) > 0
	selection.Mode = ""
	selection.Entry = ""
	selection.Hops = nil
	selection.Fallback = chains.FallbackChain
	selection.Reason = reason + "，已回退"
	return selection
}

// chainOutbounds 逐跳克隆节点并串联 detour，返回入口出站标签。
func chainOutbounds(name string, hops []string, nodes map[string]jsontext.Value) ([]jsontext.Value, string, error) {
	entries := make([]jsontext.Value, 0, len(hops))
	next := ""
	// 从出口往入口构建，保证每跳的 detour 指向已经确定的后一跳标签。
	for index := len(hops) - 1; index >= 0; index-- {
		hop := hops[index]
		tag := chainHopTag(name, index)
		var entry jsontext.Value
		var err error
		if hop == DirectTag {
			content, marshalErr := json.Marshal(map[string]any{"type": "direct", "tag": tag})
			if marshalErr != nil {
				return nil, "", marshalErr
			}
			entry = jsontext.Value(content)
		} else {
			entry, err = cloneNodeWithDetour(nodes[hop], tag, next)
			if err != nil {
				return nil, "", err
			}
		}
		entries = append(entries, entry)
		next = tag
	}
	// 反向构建后倒序，保持与链路定义一致的阅读顺序。
	slices.Reverse(entries)
	return entries, next, nil
}

// cloneNodeWithDetour 复制节点原始 JSON，替换 tag 并写入 detour。
// 原始节点定义保持不变，多个链路可以安全复用同一条上游节点。
func cloneNodeWithDetour(node jsontext.Value, tag, detour string) (jsontext.Value, error) {
	if len(node) == 0 {
		return nil, errors.New("节点定义为空: " + tag)
	}
	var document map[string]jsontext.Value
	if err := json.Unmarshal(node, &document); err != nil {
		return nil, fmt.Errorf("节点定义无效 (%s): %w", tag, err)
	}
	tagContent, err := json.Marshal(tag)
	if err != nil {
		return nil, err
	}
	document["tag"] = jsontext.Value(tagContent)
	if detour != "" {
		detourContent, marshalErr := json.Marshal(detour)
		if marshalErr != nil {
			return nil, marshalErr
		}
		document["detour"] = jsontext.Value(detourContent)
	} else {
		delete(document, "detour")
	}
	content, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return jsontext.Value(content), nil
}

// chainRules 把链路绑定到一个 clash_mode，运行时切换零重启生效。
func chainRules(mode, entry string) ([]jsontext.Value, error) {
	rule, err := json.Marshal(map[string]any{
		"clash_mode": mode,
		"action":     chainActionPtr,
		"outbound":   entry,
	})
	if err != nil {
		return nil, err
	}
	return []jsontext.Value{jsontext.Value(rule)}, nil
}

func chainHopTag(name string, index int) string {
	return fmt.Sprintf("%s%s/%d", chainTagPrefix, name, index)
}

func chainModeName(name string) string {
	return chainModePrefix + trimChainName(name)
}

// writeChainState 记录最近一次链路解析结果，供状态视图与 CLI 读取。
func writeChainState(path string, selection ChainSelection) error {
	if path == "" {
		return nil
	}
	content, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0o600)
}

// ReadChainState 读取最近一次链路解析结果。
func ReadChainState(path string) (ChainSelection, error) {
	var selection ChainSelection
	content, err := os.ReadFile(path)
	if err != nil {
		return selection, err
	}
	err = json.Unmarshal(content, &selection)
	return selection, err
}

func trimChainName(value string) string { return strings.TrimSpace(value) }
