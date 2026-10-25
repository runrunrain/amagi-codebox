package piconfig

import (
	"encoding/json"
	"errors"
	"strings"
)

// ValidateAmagiConfig 与实际保存共用，供完整配置导入在写入任何 section 前检查。
// 不物化凭据、不读取磁盘、错误信息不包含输入值。
func ValidateAmagiConfig(content string) error {
	if !json.Valid([]byte(content)) {
		return errors.New("invalid JSON: content is not valid JSON")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err != nil || obj == nil {
		return errors.New("invalid config: root must be a JSON object")
	}
	if err := validateCompaction(obj); err != nil {
		return err
	}
	return validateAdvisor(obj)
}

// 只验证新增压缩配置的形态，不以静态目录限制 pi 的动态模型注册。
// 对齐 amagi-pi resolveCompactionConfig：空对象继承、null 原生、仅已知 thinking 后缀参与拆分。
func validateCompaction(obj map[string]any) error {
	raw, exists := obj["compaction"]
	if !exists {
		return nil
	}
	compaction, ok := raw.(map[string]any)
	if !ok {
		return errors.New("invalid config: compaction must be an object")
	}
	for key := range compaction {
		if key != "model" {
			return errors.New("invalid config: compaction only allows model")
		}
	}
	rawModel, exists := compaction["model"]
	if !exists || rawModel == nil {
		return nil
	}
	model, ok := rawModel.(string)
	// ECMAScript /\s/，与插件及前端一致（U+0085 不在此集合）。
	if !ok || strings.ContainsAny(model, " \t\n\v\f\r\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff") {
		return errors.New("invalid config: compaction.model must be provider/model[:thinking] or null")
	}
	slash := strings.IndexByte(model, '/')
	if slash <= 0 || slash == len(model)-1 {
		return errors.New("invalid config: compaction.model requires provider/model")
	}
	id := model[slash+1:]
	if colon := strings.LastIndexByte(id, ':'); colon >= 0 {
		switch id[colon+1:] {
		case "off", "minimal", "low", "medium", "high", "xhigh", "max":
			id = id[:colon]
		}
	}
	if id == "" {
		return errors.New("invalid config: compaction.model requires model ID")
	}
	return nil
}
