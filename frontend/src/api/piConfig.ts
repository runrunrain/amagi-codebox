// piConfig - pi (amagi-pi) 配置 API 封装。
// 直接包装 wailsjs 绑定的 App 透传方法，供 Provider Center 的
// Pi 可视化配置组件按需调用（数据量小，无需 Pinia store）。
import { GetAmagiConfig, SaveAmagiConfig, GetAmagiConfigPath, GetPiModelCatalog, GetPiModelsConfig, SavePiModelsConfig, GetPiModelsConfigPath, GetPiAuthConfig, SavePiAuthConfig, GetPiAuthConfigPath } from '../../wailsjs/go/main/App';

export function getAmagiConfig(): Promise<string> {
  return GetAmagiConfig();
}

export function saveAmagiConfig(content: string): Promise<void> {
  return SaveAmagiConfig(content);
}

export function getAmagiConfigPath(): Promise<string> {
  return GetAmagiConfigPath();
}

/** 获取 models.json 抽取的 provider→model 目录（JSON 文本，不含密钥） */
export function getPiModelCatalog(): Promise<string> {
  return GetPiModelCatalog();
}

/** 读取 models.json 注册表全文（JSON 文本，含 apiKey，仅本地编辑用） */
export function getPiModelsConfig(): Promise<string> {
  return GetPiModelsConfig();
}

/** 保存 models.json 注册表（原子写入） */
export function savePiModelsConfig(content: string): Promise<void> {
  return SavePiModelsConfig(content);
}

/** models.json 绝对路径 */
export function getPiModelsConfigPath(): Promise<string> {
  return GetPiModelsConfigPath();
}

/** 读取 auth.json 提供商凭据全文（含明文密钥，仅本地编辑用） */
export function getPiAuthConfig(): Promise<string> {
  return GetPiAuthConfig();
}

/** 保存 auth.json（原子写入） */
export function savePiAuthConfig(content: string): Promise<void> {
  return SavePiAuthConfig(content);
}

/** auth.json 绝对路径 */
export function getPiAuthConfigPath(): Promise<string> {
  return GetPiAuthConfigPath();
}
