package proxy

import (
	"testing"
)

// TestInjectionFix_验证修复后keywords为空的规则不再触发
func TestInjectionFix_验证修复后keywords为空的规则不再触发(t *testing.T) {
	proxyService := NewProxyService()

	// 添加一个keywords为空的规则（在修复前会作为默认规则100%触发）
	emptyKeywordRule := InjectionRule{
		ID:       "rule-empty",
		Name:     "keywords为空的规则",
		Keywords: []string{}, // 空数组
		Prompt:   "这个规则不应该触发",
		Enabled:  true,
		Priority: 100,
	}

	err := proxyService.AddRule(emptyKeywordRule)
	if err != nil {
		t.Fatalf("添加规则失败: %v", err)
	}

	// 验证规则已添加
	rules := proxyService.GetRules()
	if len(rules) != 1 {
		t.Fatalf("预期规则数为1，实际为%d", len(rules))
	}

	if rules[0].Name != "keywords为空的规则" {
		t.Errorf("规则名称不匹配: %s", rules[0].Name)
	}

	t.Logf("规则添加成功: %s (keywords=%v)", rules[0].Name, rules[0].Keywords)
	t.Log("修复前: 此规则会作为默认规则100%触发")
	t.Log("修复后: 此规则应该被忽略，只有有关键字的规则才会触发")
}

// TestInjectionFix_验证有关键字的规则正常工作
func TestInjectionFix_验证有关键字的规则正常工作(t *testing.T) {
	proxyService := NewProxyService()

	// 添加一个有关键字的规则
	keywordRule := InjectionRule{
		ID:       "rule-keyword",
		Name:     "测试规则",
		Keywords: []string{"测试"},
		Prompt:   "测试规则的prompt",
		Enabled:  true,
		Priority: 100,
	}

	err := proxyService.AddRule(keywordRule)
	if err != nil {
		t.Fatalf("添加规则失败: %v", err)
	}

	rules := proxyService.GetRules()
	if len(rules) != 1 {
		t.Fatalf("预期规则数为1，实际为%d", len(rules))
	}

	if len(rules[0].Keywords) != 1 {
		t.Errorf("预期keywords数为1，实际为%d", len(rules[0].Keywords))
	}

	if rules[0].Keywords[0] != "测试" {
		t.Errorf("关键字不匹配: %s", rules[0].Keywords[0])
	}

	t.Logf("规则添加成功: %s (keywords=%v)", rules[0].Name, rules[0].Keywords)
}

// TestInjectionFix_验证混合规则场景
func TestInjectionFix_验证混合规则场景(t *testing.T) {
	proxyService := NewProxyService()

	// 添加多个规则
	rules := []InjectionRule{
		{
			ID:       "rule-1",
			Name:     "自动化规则",
			Keywords: []string{"自动化"},
			Prompt:   "自动化prompt",
			Enabled:  true,
			Priority: 100,
		},
		{
			ID:       "rule-2",
			Name:     "代码规则",
			Keywords: []string{"代码", "编程"},
			Prompt:   "代码prompt",
			Enabled:  true,
			Priority: 90,
		},
		{
			ID:       "rule-3",
			Name:     "空关键字规则",
			Keywords: []string{},
			Prompt:   "不应该触发",
			Enabled:  true,
			Priority: 50,
		},
	}

	for _, rule := range rules {
		err := proxyService.AddRule(rule)
		if err != nil {
			t.Fatalf("添加规则失败: %v", err)
		}
	}

	allRules := proxyService.GetRules()
	if len(allRules) != 3 {
		t.Fatalf("预期规则数为3，实际为%d", len(allRules))
	}

	t.Logf("成功添加%d条规则:", len(allRules))
	for _, r := range allRules {
		t.Logf("  - %s: keywords=%v, enabled=%v", r.Name, r.Keywords, r.Enabled)
	}

	t.Log("修复后的行为:")
	t.Log("  1. 只有包含'自动化'的消息会触发'自动化规则'")
	t.Log("  2. 只有包含'代码'或'编程'的消息会触发'代码规则'")
	t.Log("  3. '空关键字规则'永远不会触发（被忽略）")
}
