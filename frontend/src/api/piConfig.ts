// piConfig - pi (amagi-pi) 配置 API 封装。
// 直接包装 wailsjs 绑定的 App 透传方法，供 Provider Center 的
// Pi 可视化配置组件按需调用（数据量小，无需 Pinia store）。
import { GetAmagiConfig, SaveAmagiConfig, GetAmagiConfigPath, GetPiModelCatalog } from '../../wailsjs/go/main/App';

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
