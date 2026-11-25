package module

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"testing"

	moduleconfig "github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/config"
)

func mustNode(t *testing.T, tag, nodeType string) jsontext.Value {
	t.Helper()
	content, err := json.Marshal(map[string]any{
		"type": nodeType, "tag": tag, "server": "example.com", "server_port": 443,
	})
	if err != nil {
		t.Fatal(err)
	}
	return jsontext.Value(content)
}

// parseOutbounds 把生成结果还原为可断言的 map。
func parseOutbounds(t *testing.T, entries []jsontext.Value) []map[string]any {
	t.Helper()
	result := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		var item map[string]any
		if err := json.Unmarshal(entry, &item); err != nil {
			t.Fatalf("出站定义无效: %v", err)
		}
		result = append(result, item)
	}
	return result
}

func TestChainOutboundsSerializeDetour(t *testing.T) {
	nodes := map[string]jsontext.Value{
		"A": mustNode(t, "A", "shadowsocks"),
		"B": mustNode(t, "B", "trojan"),
		"C": mustNode(t, "C", "vless"),
	}
	entries, entry, err := chainOutbounds("home", []string{"A", "B", "C"}, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if entry != chainTagPrefix+"home/0" {
		t.Fatalf("入口标签应为首跳出站，实际 %s", entry)
	}
	outbounds := parseOutbounds(t, entries)
	if len(outbounds) != 3 {
		t.Fatalf("应为三跳生成三个出站，实际 %d", len(outbounds))
	}
	byTag := make(map[string]map[string]any, len(outbounds))
	for _, item := range outbounds {
		byTag[item["tag"].(string)] = item
	}
	// 第一跳 detour 指向第二跳，第二跳 detour 指向第三跳，第三跳不带上游。
	if got := byTag[chainTagPrefix+"home/0"]["detour"]; got != chainTagPrefix+"home/1" {
		t.Fatalf("第一跳 detour 错误: %v", got)
	}
	if got := byTag[chainTagPrefix+"home/1"]["detour"]; got != chainTagPrefix+"home/2" {
		t.Fatalf("第二跳 detour 错误: %v", got)
	}
	if _, ok := byTag[chainTagPrefix+"home/2"]["detour"]; ok {
		t.Fatal("末跳不应携带 detour")
	}
	// 克隆必须保留原始协议字段。
	if byTag[chainTagPrefix+"home/0"]["type"] != "shadowsocks" {
		t.Fatalf("协议字段丢失: %v", byTag[chainTagPrefix+"home/0"])
	}
}

func TestBuildChainPlanSkipsMissingHop(t *testing.T) {
	chains := moduleconfig.Chains{Version: 1, MinHops: 2, FallbackChain: "direct"}
	chain := moduleconfig.Chain{
		Name: "office", Enabled: true, Hops: []string{"A", "GONE", "C"}, MinHops: 2,
	}
	selection := ChainSelection{Active: true, Chain: "office", Hops: chain.Hops, Mode: chainModeName("office")}
	nodes := map[string]jsontext.Value{
		"A": mustNode(t, "A", "shadowsocks"),
		"C": mustNode(t, "C", "trojan"),
	}
	plan, err := buildChainPlan(chains, chain, selection, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.selection.Active {
		t.Fatalf("两跳可用应保持生效: %+v", plan.selection)
	}
	if len(plan.selection.Skipped) != 1 || plan.selection.Skipped[0] != "GONE" {
		t.Fatalf("失效跳未记录: %+v", plan.selection.Skipped)
	}
	if !plan.selection.Degraded {
		t.Fatal("跳过节点后应标记降级")
	}
	// 剩余 A、C 两跳必须被串联为 A -> C。
	outbounds := parseOutbounds(t, plan.outbounds)
	if outbounds[0]["tag"] != chainTagPrefix+"office/0" || outbounds[0]["detour"] != chainTagPrefix+"office/1" {
		t.Fatalf("剩余跳未正确串联: %+v", outbounds[0])
	}
}

func TestBuildChainPlanFallsBackBelowMinHops(t *testing.T) {
	chains := moduleconfig.Chains{Version: 1, MinHops: 2, FallbackChain: "direct"}
	chain := moduleconfig.Chain{
		Name: "edge", Enabled: true, Hops: []string{"A", "GONE1", "GONE2"}, MinHops: 2,
	}
	selection := ChainSelection{Active: true, Chain: "edge", Hops: chain.Hops}
	nodes := map[string]jsontext.Value{"A": mustNode(t, "A", "shadowsocks")}
	plan, err := buildChainPlan(chains, chain, selection, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if plan.selection.Active {
		t.Fatal("可用跳数不足 min_hops 时应回退")
	}
	if plan.selection.Fallback != "direct" {
		t.Fatalf("回退目标错误: %s", plan.selection.Fallback)
	}
	if len(plan.outbounds) != 0 {
		t.Fatal("回退时不应生成链路出站")
	}
}

func TestChainRulesUseClashMode(t *testing.T) {
	rules, err := chainRules("chain_home", "netproxy-chain/home/0")
	if err != nil {
		t.Fatal(err)
	}
	var rule map[string]any
	if err := json.Unmarshal(rules[0], &rule); err != nil {
		t.Fatal(err)
	}
	if rule["clash_mode"] != "chain_home" || rule["outbound"] != "netproxy-chain/home/0" {
		t.Fatalf("规则内容错误: %+v", rule)
	}
}

func TestSelectChainPrefersCarrierMatch(t *testing.T) {
	chains := moduleconfig.Chains{
		Version: 1, MinHops: 1, FallbackChain: "direct",
		Chains: []moduleconfig.Chain{
			{Name: "home", Enabled: true, Priority: 10, Hops: []string{"A"},
				Match: moduleconfig.ChainMatch{SSID: []string{"HomeWiFi"}}},
			{Name: "cmcc", Enabled: true, Priority: 20, Hops: []string{"B"},
				Match: moduleconfig.ChainMatch{MCC: []string{"460"}, MNC: []string{"02"}}},
		},
	}
	// WiFi 生效时不带运营商信息，应命中 SSID 链路。
	if chain, _ := selectChain(chains, NetworkIdentity{NetworkType: "wifi", SSID: "HomeWiFi"}); chain.Name != "home" {
		t.Fatalf("WiFi 匹配失败: %s", chain.Name)
	}
	// 移动网络按 MCC/MNC 命中运营商链路。
	if chain, _ := selectChain(chains, NetworkIdentity{NetworkType: "not_wifi", MCC: "460", MNC: "02"}); chain.Name != "cmcc" {
		t.Fatalf("运营商匹配失败: %s", chain.Name)
	}
	// 未命中任何链路时回退。
	if chain, sel := selectChain(chains, NetworkIdentity{NetworkType: "not_wifi", MCC: "460", MNC: "01"}); chain.Name != "" || sel.Fallback != "direct" {
		t.Fatalf("回退结果错误: %+v", sel)
	}
}
