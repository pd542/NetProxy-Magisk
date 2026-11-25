package module

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"os"
	"path/filepath"

	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/catalog"
	moduleconfig "github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/config"
)

// prepareChainPlan 读取链路配置与节点池，生成 sing-box 出站与路由规则。
//
// 链路管理关闭或代理总开关关闭时返回 nil，配置与旧版本完全一致。
func prepareChainPlan(ctx context.Context, options Options, runtime catalog.RuntimeResult) (*chainPlan, error) {
	chains, err := LoadChains(options)
	if err != nil {
		return nil, err
	}
	if !chains.Enabled || len(chains.Chains) == 0 {
		// 未启用链路管理：清掉残留状态，避免状态视图展示过期链路。
		_ = writeChainState(chainStatePath(options), ChainSelection{Reason: "链路管理未启用"})
		return nil, nil
	}
	module, err := moduleconfig.LoadModule(options.ModuleConfig)
	if err != nil {
		return nil, err
	}
	if !module.Proxy.Enabled {
		_ = writeChainState(chainStatePath(options), ChainSelection{Reason: "代理总开关已关闭", Disabled: true})
		return nil, nil
	}
	identity, err := readNetworkIdentity(ctx, options)
	if err != nil {
		// 网络尚不可读时按“没有命中”处理，回退链路仍可用。
		logService(options, "WARN", "chain.network", "read_failed", "网络状态读取失败: %v", err)
	}
	chain, selection := selectChain(chains, identity)
	if !module.Carrier.Enabled {
		selection.Network.MCC, selection.Network.MNC, selection.Network.ICCID = "", "", ""
	}
	if !selection.Active {
		_ = writeChainState(chainStatePath(options), selection)
		logService(options, "INFO", "chain.select", "fallback", "链路回退: %s", selection.Reason)
		return &chainPlan{selection: selection}, nil
	}
	nodes, err := loadChainNodes(ctx, options, chain.Hops)
	if err != nil {
		return nil, err
	}
	plan, err := buildChainPlan(chains, chain, selection, nodes)
	if err != nil {
		return nil, err
	}
	if !plan.selection.Active {
		// 跳数不足导致降级：记录原因并交由回退链路处理。
		logService(options, "WARN", "chain.select", "degraded", "链路降级: %s", plan.selection.Reason)
	}
	_ = writeChainState(chainStatePath(options), plan.selection)
	logService(options, "INFO", "chain.select", "active", "链路生效: %s -> %v", plan.selection.Chain, plan.selection.Hops)
	return &plan, nil
}

// loadChainNodes 从分组 provider.json 中取出链路用到的节点原始出站定义。
//
// 只加载链路实际引用的 tag，避免为一次性切换读取整个节点池。
func loadChainNodes(ctx context.Context, options Options, hops []string) (map[string]jsontext.Value, error) {
	wanted := make(map[string]bool, len(hops))
	for _, hop := range hops {
		if hop != DirectTag {
			wanted[hop] = true
		}
	}
	nodes := make(map[string]jsontext.Value, len(wanted))
	if len(wanted) == 0 {
		return nodes, nil
	}
	entries, err := os.ReadDir(options.CatalogRoot)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || len(nodes) == len(wanted) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := filepath.Join(options.CatalogRoot, entry.Name(), "provider.json")
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		var document struct {
			Outbounds []jsontext.Value `json:"outbounds"`
		}
		if err := json.Unmarshal(content, &document); err != nil {
			// 单个分组损坏不应阻断切换，链路会把这跳当作失效节点跳过。
			logService(options, "WARN", "chain.nodes", "invalid", "分组 %s 节点定义无效: %v", entry.Name(), err)
			continue
		}
		for _, outbound := range document.Outbounds {
			var head struct {
				Tag string `json:"tag"`
			}
			if err := json.Unmarshal(outbound, &head); err != nil || head.Tag == "" {
				continue
			}
			if wanted[head.Tag] {
				nodes[head.Tag] = outbound
			}
		}
	}
	return nodes, nil
}
