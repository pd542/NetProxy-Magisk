package module

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"os"

	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/paths"
)

// writeEffectiveConfig 合并入站、Provider 与链路生成物，写出最终 sing-box 配置。
func writeEffectiveConfig(ctx context.Context, singBoxDir, destination string, inbounds []jsontext.Value, providers []jsontext.Value, plans ...*chainPlan) error {
	content, err := os.ReadFile(paths.SingBoxConfig(singBoxDir))
	if err != nil {
		return err
	}
	document, err := configObject(content)
	if err != nil {
		return err
	}
	if err := validateConfiguredCatalogProviders(document, providers); err != nil {
		return err
	}
	if len(plans) > 0 && plans[0] != nil {
		if err := insertChainEntries(document, plans[0]); err != nil {
			return err
		}
	}
	for key, entries := range map[string][]jsontext.Value{
		"inbounds": inbounds, "providers": providers,
	} {
		if err := appendConfigEntries(document, key, entries); err != nil {
			return err
		}
	}
	content, err = json.Marshal(document, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeConfigAtomic(destination, append(content, '\n'), 0o600)
}

// insertChainEntries 把链路出站插到 outbounds 最前面，并把链路模式规则插到 route.rules 最前面。
//
// 顺序很重要：clash_mode 规则必须早于其他路由规则才能在切换时整体改变出口，
// 而链路出站要先于原生出站声明，便于阅读生成后的配置。
func insertChainEntries(document map[string]jsontext.Value, plan *chainPlan) error {
	if len(plan.outbounds) > 0 {
		if err := prependConfigEntries(document, "outbounds", plan.outbounds); err != nil {
			return err
		}
	}
	if len(plan.rules) > 0 {
		var route map[string]jsontext.Value
		if raw, ok := document["route"]; ok {
			if err := json.Unmarshal(raw, &route); err != nil {
				return fmt.Errorf("invalid route: %w", err)
			}
		}
		if route == nil {
			route = make(map[string]jsontext.Value)
		}
		if err := prependConfigEntries(route, "rules", plan.rules); err != nil {
			return err
		}
		content, err := json.Marshal(route)
		if err != nil {
			return err
		}
		document["route"] = jsontext.Value(content)
	}
	return nil
}

// prependConfigEntries 在指定数组前插入受管条目，保留原有顺序与无关字段。
func prependConfigEntries(document map[string]jsontext.Value, key string, managed []jsontext.Value) error {
	if len(managed) == 0 {
		return nil
	}
	var entries []jsontext.Value
	if raw, ok := document[key]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("invalid %s: %w", key, err)
		}
	}
	content, err := json.Marshal(append(managed, entries...))
	if err != nil {
		return err
	}
	document[key] = content
	return nil
}

// Append only managed arrays; keep unrelated native JSON and array ordering intact.
func appendConfigEntries(document map[string]jsontext.Value, key string, managed []jsontext.Value) error {
	if len(managed) == 0 {
		return nil
	}
	var entries []jsontext.Value
	if raw, ok := document[key]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("invalid %s: %w", key, err)
		}
	}
	content, err := json.Marshal(append(entries, managed...))
	if err != nil {
		return err
	}
	document[key] = content
	return nil
}
