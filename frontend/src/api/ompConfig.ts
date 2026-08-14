// ompConfig - OMP (oh-my-pi) 配置 API 封装。
// 直接包装 wailsjs 绑定的 App 透传方法，供 Provider Center 的
// OMP 可视化配置组件按需调用（数据量小，无需 Pinia store）。
import { GetOmpConfig, SaveOmpConfig, GetOmpConfigPath, GetOmpModelCatalog } from '../../wailsjs/go/main/App';

export function getOmpConfig(): Promise<string> {
  return GetOmpConfig();
}

export function saveOmpConfig(content: string): Promise<void> {
  return SaveOmpConfig(content);
}

export function getOmpConfigPath(): Promise<string> {
  return GetOmpConfigPath();
}

/** 获取 models.yml 抽取的 provider→model 目录（JSON 文本，不含密钥） */
export function getOmpModelCatalog(): Promise<string> {
  return GetOmpModelCatalog();
}
