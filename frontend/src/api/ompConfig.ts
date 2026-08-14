// ompConfig - OMP (oh-my-pi) 配置 API 封装。
// 直接包装 wailsjs 绑定的 App 透传方法，供 Provider Center 的
// OMP 可视化配置组件按需调用（数据量小，无需 Pinia store）。
import { GetOmpConfig, SaveOmpConfig, GetOmpConfigPath, GetOmpModelCatalog, GetOmpModelsConfig, SaveOmpModelsConfig, GetOmpModelsConfigPath } from '../../wailsjs/go/main/App';

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

/** 读取 models.yml 注册表全文（YAML 文本，含 apiKey，仅本地编辑用） */
export function getOmpModelsConfig(): Promise<string> {
  return GetOmpModelsConfig();
}

/** 保存 models.yml 注册表（原子写入） */
export function saveOmpModelsConfig(content: string): Promise<void> {
  return SaveOmpModelsConfig(content);
}

/** models.yml 绝对路径 */
export function getOmpModelsConfigPath(): Promise<string> {
  return GetOmpModelsConfigPath();
}
